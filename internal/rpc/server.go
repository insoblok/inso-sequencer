package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-sequencer/internal/config"
)

// Server is the JSON-RPC HTTP and WebSocket server.
type Server struct {
	httpServer *http.Server
	wsServer   *http.Server
	handler    *Handler
	logger     log.Logger
	cfg        *config.SequencerConfig
}

// NewServer creates a new RPC server.
func NewServer(cfg *config.SequencerConfig, handler *Handler) *Server {
	return &Server{
		handler: handler,
		logger:  log.New("module", "rpc"),
		cfg:     cfg,
	}
}

// Start begins listening for JSON-RPC requests on HTTP and WebSocket.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleHTTP)
	mux.HandleFunc("/health", s.handleHealth)

	s.httpServer = &http.Server{
		Addr:         s.cfg.ListenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		BaseContext:  func(_ net.Listener) context.Context { return ctx },
	}

	// WebSocket server
	wsMux := http.NewServeMux()
	wsMux.HandleFunc("/", s.handleWS)

	s.wsServer = &http.Server{
		Addr:         s.cfg.WSAddr,
		Handler:      wsMux,
		ReadTimeout:  0, // no timeout for WS
		WriteTimeout: 0,
		BaseContext:  func(_ net.Listener) context.Context { return ctx },
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		s.logger.Info("JSON-RPC HTTP server starting", "addr", s.cfg.ListenAddr)
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("http server: %w", err)
		}
	}()

	go func() {
		defer wg.Done()
		s.logger.Info("JSON-RPC WebSocket server starting", "addr", s.cfg.WSAddr)
		if err := s.wsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- fmt.Errorf("ws server: %w", err)
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-time.After(100 * time.Millisecond):
		// Servers started successfully
		return nil
	}
}

// Stop gracefully shuts down both servers.
func (s *Server) Stop(ctx context.Context) error {
	s.logger.Info("Shutting down RPC servers")
	var err1, err2 error
	if s.httpServer != nil {
		err1 = s.httpServer.Shutdown(ctx)
	}
	if s.wsServer != nil {
		err2 = s.wsServer.Shutdown(ctx)
	}
	if err1 != nil {
		return err1
	}
	return err2
}

// handleHTTP processes incoming JSON-RPC HTTP requests.
func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB limit
	if err != nil {
		s.writeError(w, nil, -32700, "parse error")
		return
	}
	defer r.Body.Close()

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeError(w, nil, -32700, "parse error")
		return
	}

	resp := s.handler.Handle(r.Context(), &req)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleWS handles WebSocket connections (upgrade + message loop).
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	// WebSocket upgrade and message loop would use gorilla/websocket or nhooyr.io/websocket.
	// For now, fall back to HTTP long-polling style.
	s.handleHTTP(w, r)
}

// handleHealth returns a simple health check response.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"service": "inso-sequencer",
	})
}

func (s *Server) writeError(w http.ResponseWriter, id interface{}, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	resp := &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: msg,
		},
	}
	json.NewEncoder(w).Encode(resp)
}
