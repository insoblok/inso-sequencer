package rpc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/ethereum/go-ethereum/log"
	"github.com/gorilla/websocket"
)

// WSSubscriptionManager manages WebSocket connections and subscriptions.
type WSSubscriptionManager struct {
	mu          sync.RWMutex
	subscribers map[uint64]*wsSubscription
	nextID      atomic.Uint64
	handler     *Handler
	logger      log.Logger
	upgrader    websocket.Upgrader
}

type wsSubscription struct {
	id       uint64
	conn     *websocket.Conn
	subType  string // "newHeads", "logs", "newPendingTransactions"
	mu       sync.Mutex
	closed   bool
}

// NewWSSubscriptionManager creates a new WebSocket subscription manager.
func NewWSSubscriptionManager(handler *Handler) *WSSubscriptionManager {
	return &WSSubscriptionManager{
		subscribers: make(map[uint64]*wsSubscription),
		handler:     handler,
		logger:      log.New("module", "ws"),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for devnet
			},
		},
	}
}

// HandleWS upgrades an HTTP connection to WebSocket and manages subscriptions.
func (m *WSSubscriptionManager) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := m.upgrader.Upgrade(w, r, nil)
	if err != nil {
		m.logger.Error("WebSocket upgrade failed", "err", err)
		return
	}
	defer conn.Close()

	m.logger.Debug("WebSocket connection established", "remote", r.RemoteAddr)

	// Read messages from the client
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				m.logger.Debug("WebSocket read error", "err", err)
			}
			m.cleanupConn(conn)
			return
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(message, &req); err != nil {
			m.writeWSError(conn, nil, -32700, "parse error")
			continue
		}

		switch req.Method {
		case "eth_subscribe":
			m.handleSubscribe(conn, &req)
		case "eth_unsubscribe":
			m.handleUnsubscribe(conn, &req)
		default:
			// Handle regular RPC call over WebSocket
			resp := m.handler.Handle(r.Context(), &req)
			m.writeWSResponse(conn, resp)
		}
	}
}

// handleSubscribe processes an eth_subscribe request.
func (m *WSSubscriptionManager) handleSubscribe(conn *websocket.Conn, req *JSONRPCRequest) {
	var params []json.RawMessage
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) == 0 {
		m.writeWSError(conn, req.ID, -32602, "invalid subscription type")
		return
	}

	var subType string
	if err := json.Unmarshal(params[0], &subType); err != nil {
		m.writeWSError(conn, req.ID, -32602, "invalid subscription type")
		return
	}

	// Validate subscription type
	switch subType {
	case "newHeads", "logs", "newPendingTransactions":
		// supported
	default:
		m.writeWSError(conn, req.ID, -32602, fmt.Sprintf("unsupported subscription type: %s", subType))
		return
	}

	subID := m.nextID.Add(1)
	sub := &wsSubscription{
		id:      subID,
		conn:    conn,
		subType: subType,
	}

	m.mu.Lock()
	m.subscribers[subID] = sub
	m.mu.Unlock()

	m.logger.Debug("New subscription", "id", subID, "type", subType, "remote", conn.RemoteAddr())

	// Return the subscription ID
	subIDStr := fmt.Sprintf("0x%x", subID)
	subIDJSON, _ := json.Marshal(subIDStr)
	subIDRaw := json.RawMessage(subIDJSON)
	resp := &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  &subIDRaw,
	}
	m.writeWSResponse(conn, resp)
}

// handleUnsubscribe processes an eth_unsubscribe request.
func (m *WSSubscriptionManager) handleUnsubscribe(conn *websocket.Conn, req *JSONRPCRequest) {
	var params []json.RawMessage
	if err := json.Unmarshal(req.Params, &params); err != nil || len(params) == 0 {
		m.writeWSError(conn, req.ID, -32602, "missing subscription id")
		return
	}

	var subIDHex string
	if err := json.Unmarshal(params[0], &subIDHex); err != nil {
		m.writeWSError(conn, req.ID, -32602, "invalid subscription id")
		return
	}

	var subID uint64
	fmt.Sscanf(subIDHex, "0x%x", &subID)

	m.mu.Lock()
	sub, exists := m.subscribers[subID]
	if exists {
		sub.mu.Lock()
		sub.closed = true
		sub.mu.Unlock()
		delete(m.subscribers, subID)
	}
	m.mu.Unlock()

	existsJSON, _ := json.Marshal(exists)
	existsRaw := json.RawMessage(existsJSON)
	resp := &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  &existsRaw,
	}
	m.writeWSResponse(conn, resp)
}

// BroadcastNewHead sends a newHeads notification to all subscribed clients.
func (m *WSSubscriptionManager) BroadcastNewHead(head interface{}) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, sub := range m.subscribers {
		if sub.subType != "newHeads" {
			continue
		}
		sub.mu.Lock()
		if sub.closed {
			sub.mu.Unlock()
			continue
		}
		notification := map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "eth_subscription",
			"params": map[string]interface{}{
				"subscription": fmt.Sprintf("0x%x", sub.id),
				"result":       head,
			},
		}
		data, _ := json.Marshal(notification)
		if err := sub.conn.WriteMessage(websocket.TextMessage, data); err != nil {
			sub.closed = true
			m.logger.Debug("Failed to write to subscriber", "id", sub.id, "err", err)
		}
		sub.mu.Unlock()
	}
}

// BroadcastNewPendingTx sends a newPendingTransactions notification.
func (m *WSSubscriptionManager) BroadcastNewPendingTx(txHash string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, sub := range m.subscribers {
		if sub.subType != "newPendingTransactions" {
			continue
		}
		sub.mu.Lock()
		if sub.closed {
			sub.mu.Unlock()
			continue
		}
		notification := map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "eth_subscription",
			"params": map[string]interface{}{
				"subscription": fmt.Sprintf("0x%x", sub.id),
				"result":       txHash,
			},
		}
		data, _ := json.Marshal(notification)
		if err := sub.conn.WriteMessage(websocket.TextMessage, data); err != nil {
			sub.closed = true
		}
		sub.mu.Unlock()
	}
}

// SubscriberCount returns the number of active subscriptions.
func (m *WSSubscriptionManager) SubscriberCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.subscribers)
}

// cleanupConn removes all subscriptions for a disconnected connection.
func (m *WSSubscriptionManager) cleanupConn(conn *websocket.Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, sub := range m.subscribers {
		if sub.conn == conn {
			sub.mu.Lock()
			sub.closed = true
			sub.mu.Unlock()
			delete(m.subscribers, id)
		}
	}
}

func (m *WSSubscriptionManager) writeWSResponse(conn *websocket.Conn, resp *JSONRPCResponse) {
	data, _ := json.Marshal(resp)
	conn.WriteMessage(websocket.TextMessage, data)
}

func (m *WSSubscriptionManager) writeWSError(conn *websocket.Conn, id interface{}, code int, msg string) {
	resp := &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: msg},
	}
	m.writeWSResponse(conn, resp)
}
