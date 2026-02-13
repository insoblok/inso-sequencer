package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"

	"github.com/insoblok/inso-sequencer/internal/mempool"
	"github.com/insoblok/inso-sequencer/internal/state"
	"github.com/insoblok/inso-sequencer/internal/tastescore"
)

// Handler dispatches JSON-RPC methods to their implementations.
type Handler struct {
	mempool    *mempool.Mempool
	state      *state.Manager
	tastescore *tastescore.Client
	chainID    *big.Int
	logger     log.Logger
}

// NewHandler creates a new JSON-RPC handler.
func NewHandler(mp *mempool.Mempool, sm *state.Manager, ts *tastescore.Client, chainID uint64) *Handler {
	return &Handler{
		mempool:    mp,
		state:      sm,
		tastescore: ts,
		chainID:    new(big.Int).SetUint64(chainID),
		logger:     log.New("module", "rpc-handler"),
	}
}

// Handle processes a single JSON-RPC request and returns a response.
func (h *Handler) Handle(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	h.logger.Debug("RPC request", "method", req.Method, "id", req.ID)

	var result interface{}
	var err error

	switch req.Method {
	// Standard Ethereum methods
	case "eth_chainId":
		result = hexutil.EncodeBig(h.chainID)
	case "eth_blockNumber":
		result = hexutil.EncodeUint64(h.state.CurrentBlock())
	case "eth_getBlockByNumber":
		result, err = h.getBlockByNumber(req.Params)
	case "eth_getBlockByHash":
		result, err = h.getBlockByHash(req.Params)
	case "eth_sendRawTransaction":
		result, err = h.sendRawTransaction(ctx, req.Params)
	case "eth_getTransactionReceipt":
		result, err = h.getTransactionReceipt(req.Params)
	case "eth_getTransactionByHash":
		result, err = h.getTransactionByHash(req.Params)
	case "eth_getBalance":
		result, err = h.getBalance(req.Params)
	case "eth_getTransactionCount":
		result, err = h.getTransactionCount(req.Params)
	case "eth_gasPrice":
		result = hexutil.EncodeBig(big.NewInt(1_000_000_000)) // 1 gwei fixed
	case "eth_estimateGas":
		result = hexutil.EncodeUint64(21000) // simplified estimate
	case "net_version":
		result = fmt.Sprintf("%d", h.chainID.Uint64())

	// InSoBlok custom methods
	case "inso_getSequencerStatus":
		result = h.getSequencerStatus()
	case "inso_getTasteScoreOrdering":
		result = h.getTasteScoreOrdering()
	case "inso_getBatchStatus":
		result = h.getBatchStatus()
	case "inso_getPendingTxCount":
		result = h.mempool.Len()

	default:
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32601, Message: fmt.Sprintf("method %s not found", req.Method)},
		}
	}

	if err != nil {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32000, Message: err.Error()},
		}
	}

	encoded, _ := json.Marshal(result)
	raw := json.RawMessage(encoded)
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  &raw,
	}
}

// --- Standard Ethereum methods ---

func (h *Handler) sendRawTransaction(ctx context.Context, params json.RawMessage) (interface{}, error) {
	var args []string
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params")
	}

	rawTx, err := hexutil.Decode(args[0])
	if err != nil {
		return nil, fmt.Errorf("invalid hex data: %w", err)
	}

	var tx types.Transaction
	if err := rlp.DecodeBytes(rawTx, &tx); err != nil {
		return nil, fmt.Errorf("invalid transaction: %w", err)
	}

	// Derive sender
	signer := types.LatestSignerForChainID(h.chainID)
	sender, err := types.Sender(signer, &tx)
	if err != nil {
		return nil, fmt.Errorf("invalid sender: %w", err)
	}

	// Get TasteScore for sender
	var score float64
	if h.tastescore != nil {
		score, _ = h.tastescore.GetScore(ctx, sender)
	}

	// Add to mempool
	if err := h.mempool.Add(&tx, sender, score); err != nil {
		return nil, fmt.Errorf("mempool reject: %w", err)
	}

	return tx.Hash().Hex(), nil
}

func (h *Handler) getBlockByNumber(params json.RawMessage) (interface{}, error) {
	var args []json.RawMessage
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params")
	}

	var blockNumStr string
	if err := json.Unmarshal(args[0], &blockNumStr); err != nil {
		return nil, fmt.Errorf("invalid block number")
	}

	var blockNum uint64
	if blockNumStr == "latest" {
		blockNum = h.state.CurrentBlock()
	} else {
		n, err := hexutil.DecodeUint64(blockNumStr)
		if err != nil {
			return nil, fmt.Errorf("invalid block number: %w", err)
		}
		blockNum = n
	}

	block := h.state.GetBlock(blockNum)
	if block == nil {
		return nil, nil
	}
	return block, nil
}

func (h *Handler) getBlockByHash(params json.RawMessage) (interface{}, error) {
	var args []json.RawMessage
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params")
	}

	var hashStr string
	if err := json.Unmarshal(args[0], &hashStr); err != nil {
		return nil, fmt.Errorf("invalid block hash")
	}

	hash := common.HexToHash(hashStr)
	block := h.state.GetBlockByHash(hash)
	if block == nil {
		return nil, nil
	}
	return block, nil
}

func (h *Handler) getTransactionReceipt(params json.RawMessage) (interface{}, error) {
	var args []string
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params")
	}

	hash := common.HexToHash(args[0])
	receipt := h.state.GetReceipt(hash)
	return receipt, nil
}

func (h *Handler) getTransactionByHash(params json.RawMessage) (interface{}, error) {
	var args []string
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params")
	}

	hash := common.HexToHash(args[0])
	tx := h.state.GetTransaction(hash)
	return tx, nil
}

func (h *Handler) getBalance(params json.RawMessage) (interface{}, error) {
	var args []string
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params")
	}

	addr := common.HexToAddress(args[0])
	balance := h.state.GetBalance(addr)
	return hexutil.EncodeBig(balance), nil
}

func (h *Handler) getTransactionCount(params json.RawMessage) (interface{}, error) {
	var args []string
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params")
	}

	addr := common.HexToAddress(args[0])
	nonce := h.state.GetNonce(addr)
	return hexutil.EncodeUint64(nonce), nil
}

// --- InSoBlok custom methods ---

func (h *Handler) getSequencerStatus() interface{} {
	return h.state.GetSequencerStatus()
}

func (h *Handler) getTasteScoreOrdering() interface{} {
	pending := h.mempool.Pending()
	type entry struct {
		TxHash     string  `json:"txHash"`
		Sender     string  `json:"sender"`
		TasteScore float64 `json:"tasteScore"`
		Priority   float64 `json:"priority"`
		GasPrice   string  `json:"gasPrice"`
	}

	entries := make([]entry, 0, len(pending))
	for _, meta := range pending {
		entries = append(entries, entry{
			TxHash:     meta.Tx.Hash().Hex(),
			Sender:     meta.Sender.Hex(),
			TasteScore: meta.TasteScore,
			Priority:   meta.Priority,
			GasPrice:   meta.GasPrice.String(),
		})
	}
	return entries
}

func (h *Handler) getBatchStatus() interface{} {
	return h.state.GetLatestBatch()
}
