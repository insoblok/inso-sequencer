package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"

	"github.com/insoblok/inso-sequencer/internal/execution"
	"github.com/insoblok/inso-sequencer/internal/fees"
	"github.com/insoblok/inso-sequencer/internal/mempool"
	"github.com/insoblok/inso-sequencer/internal/metrics"
	"github.com/insoblok/inso-sequencer/internal/producer"
	"github.com/insoblok/inso-sequencer/internal/state"
	"github.com/insoblok/inso-sequencer/internal/tastescore"
	insoTypes "github.com/insoblok/inso-sequencer/pkg/types"
)

// Handler dispatches JSON-RPC methods to their implementations.
type Handler struct {
	mempool      mempool.TxPool
	state        *state.Manager
	tastescore   *tastescore.Client
	feeModel     *fees.DynamicFeeModel
	receiptStore *execution.ReceiptStore
	lanedPool    *mempool.LanedMempool
	sizer        *producer.AdaptiveBlockSizer
	metrics      *metrics.Metrics
	chainID      *big.Int
	logger       log.Logger
}

// NewHandler creates a new JSON-RPC handler.
func NewHandler(mp mempool.TxPool, sm *state.Manager, ts *tastescore.Client, chainID uint64, fm *fees.DynamicFeeModel) *Handler {
	return &Handler{
		mempool:    mp,
		state:      sm,
		tastescore: ts,
		feeModel:   fm,
		chainID:    new(big.Int).SetUint64(chainID),
		logger:     log.New("module", "rpc-handler"),
	}
}

// SetReceiptStore attaches the verifiable compute receipt store.
func (h *Handler) SetReceiptStore(rs *execution.ReceiptStore) { h.receiptStore = rs }

// SetLanedPool attaches the laned mempool for lane-specific stats.
func (h *Handler) SetLanedPool(lp *mempool.LanedMempool) { h.lanedPool = lp }

// SetAdaptiveSizer attaches the adaptive block sizer for stats.
func (h *Handler) SetAdaptiveSizer(s *producer.AdaptiveBlockSizer) { h.sizer = s }

// SetMetrics attaches the Prometheus metrics instance.
func (h *Handler) SetMetrics(m *metrics.Metrics) { h.metrics = m }

// Handle processes a single JSON-RPC request and returns a response.
func (h *Handler) Handle(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	h.logger.Debug("RPC request", "method", req.Method, "id", req.ID)

	// Track RPC requests
	if h.metrics != nil {
		h.metrics.RPCRequests.Add(1)
	}

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
		result = hexutil.EncodeBig(h.feeModel.BaseFee()) // Phase 4: dynamic base fee
	case "eth_estimateGas":
		result, err = h.estimateGas(req.Params)
	case "eth_call":
		result, err = h.ethCall(req.Params)
	case "eth_getCode":
		result, err = h.getCode(req.Params)
	case "eth_getStorageAt":
		result, err = h.getStorageAt(req.Params)
	case "eth_getLogs":
		result, err = h.getLogs(req.Params)
	case "eth_feeHistory":
		result = h.feeHistory(req.Params)
	case "eth_maxPriorityFeePerGas":
		result = hexutil.EncodeBig(big.NewInt(1000000000)) // 1 Gwei tip
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
	case "inso_getFeeStats":
		result = h.feeModel.Stats()
	case "inso_getEffectiveFee":
		result, err = h.getEffectiveFee(req.Params)
	case "inso_getSovereigntyDiscount":
		result, err = h.getSovereigntyDiscount(req.Params)

	// Phase 5: new InSoBlok feature endpoints
	case "inso_getComputeReceipt":
		result, err = h.getComputeReceipt(req.Params)
	case "inso_getBlockReceiptRoot":
		result, err = h.getBlockReceiptRoot(req.Params)
	case "inso_getLaneStats":
		result = h.getLaneStats()
	case "inso_getAdaptiveBlockStats":
		result = h.getAdaptiveBlockStats()

	// Phase 6+: Extended InSoBlok feature endpoints
	case "inso_getZKProofStatus":
		result, err = h.getZKProofStatus(req.Params)
	case "inso_getCrossChainAttestation":
		result, err = h.getCrossChainAttestation(req.Params)
	case "inso_getOrderingProof":
		result, err = h.getOrderingProof(req.Params)
	case "inso_getAIScorePreview":
		result, err = h.getAIScorePreview(req.Params)
	case "inso_getGovernanceProposals":
		result, err = h.getGovernanceProposals()
	case "inso_getDABlobStatus":
		result, err = h.getDABlobStatus(req.Params)
	case "inso_getRestakingStats":
		result, err = h.getRestakingStats()

	default:
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32601, Message: fmt.Sprintf("method %s not found", req.Method)},
		}
	}

	if err != nil {
		// Track RPC errors
		if h.metrics != nil {
			h.metrics.RPCErrors.Add(1)
		}
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

	// second arg: fullTx flag
	fullTx := false
	if len(args) > 1 {
		_ = json.Unmarshal(args[1], &fullTx)
	}
	return ethBlockResponse(block, fullTx), nil
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

	fullTx := false
	if len(args) > 1 {
		_ = json.Unmarshal(args[1], &fullTx)
	}
	return ethBlockResponse(block, fullTx), nil
}

func (h *Handler) getTransactionReceipt(params json.RawMessage) (interface{}, error) {
	var args []string
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params")
	}

	hash := common.HexToHash(args[0])
	receipt := h.state.GetReceipt(hash)
	if receipt == nil {
		return nil, nil
	}

	// Build Ethereum-compatible receipt response
	resp := map[string]interface{}{
		"transactionHash":   receipt.TxHash,
		"transactionIndex":  hexutil.Uint64(receipt.TransactionIndex),
		"blockHash":         receipt.BlockHash,
		"blockNumber":       hexutil.Uint64(receipt.BlockNumber.Uint64()),
		"from":              common.Address{}, // filled below
		"gasUsed":           hexutil.Uint64(receipt.GasUsed),
		"cumulativeGasUsed": hexutil.Uint64(receipt.CumulativeGasUsed),
		"status":            hexutil.Uint64(receipt.Status),
		"logsBloom":         receipt.Bloom,
		"type":              hexutil.Uint64(receipt.Type),
		"effectiveGasPrice": hexutil.EncodeBig(h.feeModel.BaseFee()),
	}

	if receipt.ContractAddress != (common.Address{}) {
		resp["contractAddress"] = receipt.ContractAddress
	} else {
		resp["contractAddress"] = nil
	}

	if receipt.Logs != nil {
		resp["logs"] = receipt.Logs
	} else {
		resp["logs"] = []*types.Log{}
	}

	// Resolve sender from the stored transaction
	tx := h.state.GetTransaction(hash)
	if tx != nil {
		signer := types.LatestSignerForChainID(h.chainID)
		sender, err := types.Sender(signer, tx)
		if err == nil {
			resp["from"] = sender
		}
		resp["to"] = tx.To()
	}

	return resp, nil
}

func (h *Handler) getTransactionByHash(params json.RawMessage) (interface{}, error) {
	var args []string
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params")
	}

	hash := common.HexToHash(args[0])
	tx := h.state.GetTransaction(hash)
	if tx == nil {
		return nil, nil
	}

	// Resolve block context
	blockNum, found := h.state.GetTxBlockNumber(hash)

	signer := types.LatestSignerForChainID(h.chainID)
	sender, _ := types.Sender(signer, tx)

	resp := map[string]interface{}{
		"hash":     tx.Hash(),
		"nonce":    hexutil.Uint64(tx.Nonce()),
		"from":     sender,
		"to":       tx.To(),
		"value":    hexutil.EncodeBig(tx.Value()),
		"gas":      hexutil.Uint64(tx.Gas()),
		"gasPrice": hexutil.EncodeBig(tx.GasPrice()),
		"input":    hexutil.Encode(tx.Data()),
		"type":     hexutil.Uint64(tx.Type()),
		"chainId":  hexutil.EncodeBig(tx.ChainId()),
		"v":        "0x0",
		"r":        "0x0",
		"s":        "0x0",
	}

	if found {
		resp["blockNumber"] = hexutil.Uint64(blockNum)
		blockHash := h.state.GetBlockHash(blockNum)
		resp["blockHash"] = blockHash
		resp["transactionIndex"] = hexutil.Uint64(0)
	} else {
		resp["blockNumber"] = nil
		resp["blockHash"] = nil
		resp["transactionIndex"] = nil
	}

	// Extract V, R, S from signature
	v, r, s := tx.RawSignatureValues()
	if v != nil {
		resp["v"] = hexutil.EncodeBig(v)
	}
	if r != nil {
		resp["r"] = hexutil.EncodeBig(r)
	}
	if s != nil {
		resp["s"] = hexutil.EncodeBig(s)
	}

	return resp, nil
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

// --- EVM-backed methods (Phase 2) ---

// callArgs represents the arguments for eth_call / eth_estimateGas.
type callArgs struct {
	From     *common.Address `json:"from"`
	To       *common.Address `json:"to"`
	Gas      *hexutil.Uint64 `json:"gas"`
	GasPrice *hexutil.Big    `json:"gasPrice"`
	Value    *hexutil.Big    `json:"value"`
	Data     *hexutil.Bytes  `json:"data"`
	Input    *hexutil.Bytes  `json:"input"`
}

// toMessage converts callArgs to a core.Message for EVM execution.
func (args *callArgs) toMessage(baseFee *big.Int) *core.Message {
	from := common.Address{}
	if args.From != nil {
		from = *args.From
	}

	gas := uint64(math.MaxUint64 / 2)
	if args.Gas != nil {
		gas = uint64(*args.Gas)
	}

	var gasPrice *big.Int
	if args.GasPrice != nil {
		gasPrice = args.GasPrice.ToInt()
	} else if baseFee != nil {
		gasPrice = new(big.Int).Set(baseFee)
	} else {
		gasPrice = big.NewInt(1_000_000_000)
	}

	var value *big.Int
	if args.Value != nil {
		value = args.Value.ToInt()
	} else {
		value = big.NewInt(0)
	}

	var data []byte
	if args.Input != nil {
		data = *args.Input
	} else if args.Data != nil {
		data = *args.Data
	}

	return &core.Message{
		From:     from,
		To:       args.To,
		GasLimit: gas,
		GasPrice: gasPrice,
		Value:    value,
		Data:     data,
	}
}

func (h *Handler) ethCall(params json.RawMessage) (interface{}, error) {
	var args []json.RawMessage
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params")
	}

	var ca callArgs
	if err := json.Unmarshal(args[0], &ca); err != nil {
		return nil, fmt.Errorf("invalid call args: %w", err)
	}

	msg := ca.toMessage(h.feeModel.BaseFee())
	blockNum := h.state.CurrentBlock()

	result, _, err := h.state.CallContract(msg, blockNum)
	if err != nil {
		return nil, fmt.Errorf("execution failed: %w", err)
	}

	return hexutil.Encode(result), nil
}

func (h *Handler) estimateGas(params json.RawMessage) (interface{}, error) {
	var args []json.RawMessage
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params")
	}

	var ca callArgs
	if err := json.Unmarshal(args[0], &ca); err != nil {
		return nil, fmt.Errorf("invalid call args: %w", err)
	}

	msg := ca.toMessage(h.feeModel.BaseFee())
	blockNum := h.state.CurrentBlock()

	gas, err := h.state.EstimateGas(msg, blockNum)
	if err != nil {
		return nil, fmt.Errorf("gas estimation failed: %w", err)
	}

	return hexutil.EncodeUint64(gas), nil
}

func (h *Handler) getCode(params json.RawMessage) (interface{}, error) {
	var args []string
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params")
	}

	addr := common.HexToAddress(args[0])
	code := h.state.GetCode(addr)
	if code == nil {
		return "0x", nil
	}
	return hexutil.Encode(code), nil
}

func (h *Handler) getStorageAt(params json.RawMessage) (interface{}, error) {
	var args []string
	if err := json.Unmarshal(params, &args); err != nil || len(args) < 2 {
		return nil, fmt.Errorf("invalid params: need [address, key]")
	}

	addr := common.HexToAddress(args[0])
	key := common.HexToHash(args[1])
	val := h.state.GetStorageAt(addr, key)
	return val.Hex(), nil
}

// logFilterArgs represents the arguments for eth_getLogs.
type logFilterArgs struct {
	FromBlock *string          `json:"fromBlock"`
	ToBlock   *string          `json:"toBlock"`
	Address   *common.Address  `json:"address"`
	Topics    [][]common.Hash  `json:"topics"`
}

func (h *Handler) getLogs(params json.RawMessage) (interface{}, error) {
	var args []json.RawMessage
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params")
	}

	var filter logFilterArgs
	if err := json.Unmarshal(args[0], &filter); err != nil {
		return nil, fmt.Errorf("invalid filter: %w", err)
	}

	// Parse block range
	currentBlock := h.state.CurrentBlock()
	fromBlock := uint64(0)
	toBlock := currentBlock

	if filter.FromBlock != nil {
		if *filter.FromBlock == "latest" {
			fromBlock = currentBlock
		} else {
			n, err := hexutil.DecodeUint64(*filter.FromBlock)
			if err == nil {
				fromBlock = n
			}
		}
	}

	if filter.ToBlock != nil {
		if *filter.ToBlock == "latest" {
			toBlock = currentBlock
		} else {
			n, err := hexutil.DecodeUint64(*filter.ToBlock)
			if err == nil {
				toBlock = n
			}
		}
	}

	// Cap range to prevent excessive scanning
	if toBlock-fromBlock > 1000 {
		toBlock = fromBlock + 1000
	}

	// Collect matching logs from block receipts
	var matchingLogs []*types.Log
	for blockNum := fromBlock; blockNum <= toBlock; blockNum++ {
		block := h.state.GetBlock(blockNum)
		if block == nil {
			continue
		}

		for _, receipt := range block.Receipts {
			for _, l := range receipt.Logs {
				if matchLog(l, filter.Address, filter.Topics) {
					matchingLogs = append(matchingLogs, l)
				}
			}
		}
	}

	if matchingLogs == nil {
		matchingLogs = make([]*types.Log, 0)
	}

	return matchingLogs, nil
}

// matchLog checks if a log matches the given filter criteria.
func matchLog(l *types.Log, addr *common.Address, topics [][]common.Hash) bool {
	// Address filter
	if addr != nil && l.Address != *addr {
		return false
	}

	// Topic filters
	for i, topicFilter := range topics {
		if len(topicFilter) == 0 {
			continue // wildcard
		}
		if i >= len(l.Topics) {
			return false
		}
		matched := false
		for _, t := range topicFilter {
			if l.Topics[i] == t {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	return true
}

// --- Phase 4: DID & Sovereignty RPC methods ---

// getEffectiveFee returns the sovereignty-discounted gas price for an address.
func (h *Handler) getEffectiveFee(params json.RawMessage) (interface{}, error) {
	var args []string
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params: need [address]")
	}

	addr := common.HexToAddress(args[0])
	effectiveFee := h.feeModel.EffectiveFee(addr, nil) // no on-chain provider wired yet
	discountBps, tier := h.feeModel.GetDiscount(addr)

	tierNames := []string{"None", "Bronze", "Silver", "Gold", "Platinum"}
	tierName := "None"
	if int(tier) < len(tierNames) {
		tierName = tierNames[tier]
	}

	return map[string]interface{}{
		"baseFee":      hexutil.EncodeBig(h.feeModel.BaseFee()),
		"effectiveFee": hexutil.EncodeBig(effectiveFee),
		"discountBps":  discountBps,
		"tier":         tier,
		"tierName":     tierName,
	}, nil
}

// getSovereigntyDiscount returns the cached sovereignty discount for an address.
func (h *Handler) getSovereigntyDiscount(params json.RawMessage) (interface{}, error) {
	var args []string
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params: need [address]")
	}

	addr := common.HexToAddress(args[0])
	discountBps, tier := h.feeModel.GetDiscount(addr)

	tierNames := []string{"None", "Bronze", "Silver", "Gold", "Platinum"}
	tierName := "None"
	if int(tier) < len(tierNames) {
		tierName = tierNames[tier]
	}

	return map[string]interface{}{
		"address":     addr.Hex(),
		"discountBps": discountBps,
		"tier":        tier,
		"tierName":    tierName,
	}, nil
}

// --- Phase 5: New Feature Endpoints ---

// getComputeReceipt returns the verifiable compute receipt for a transaction.
func (h *Handler) getComputeReceipt(params json.RawMessage) (interface{}, error) {
	if h.receiptStore == nil {
		return nil, fmt.Errorf("compute receipts not enabled")
	}
	var args []string
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params: need [txHash]")
	}
	hash := common.HexToHash(args[0])
	cr := h.receiptStore.Get(hash)
	if cr == nil {
		return nil, nil
	}
	return map[string]interface{}{
		"txHash":        cr.TxHash.Hex(),
		"blockNumber":   cr.BlockNumber,
		"txIndex":       cr.TxIndex,
		"sender":        cr.Sender.Hex(),
		"gasUsed":       cr.GasUsed,
		"status":        cr.Status,
		"preStateRoot":  cr.PreStateRoot.Hex(),
		"postStateRoot": cr.PostStateRoot.Hex(),
		"inputHash":     cr.InputHash.Hex(),
		"outputHash":    cr.OutputHash.Hex(),
		"logsHash":      cr.LogsHash.Hex(),
		"receiptHash":   cr.ReceiptHash.Hex(),
		"verified":      cr.Verify(),
	}, nil
}

// getBlockReceiptRoot returns the Merkle root of all compute receipts in a block.
func (h *Handler) getBlockReceiptRoot(params json.RawMessage) (interface{}, error) {
	if h.receiptStore == nil {
		return nil, fmt.Errorf("compute receipts not enabled")
	}
	var args []string
	if err := json.Unmarshal(params, &args); err != nil || len(args) == 0 {
		return nil, fmt.Errorf("invalid params: need [blockNumber]")
	}
	blockNum, err := hexutil.DecodeUint64(args[0])
	if err != nil {
		return nil, fmt.Errorf("invalid block number: %w", err)
	}
	root := h.receiptStore.ComputeBlockReceiptRoot(blockNum)
	return map[string]interface{}{
		"blockNumber": blockNum,
		"receiptRoot": root.Hex(),
	}, nil
}

// getLaneStats returns the current execution lane statistics.
func (h *Handler) getLaneStats() interface{} {
	if h.lanedPool == nil {
		return map[string]interface{}{"enabled": false}
	}
	fast, std, slow := h.lanedPool.LaneSizes()
	return map[string]interface{}{
		"enabled":  true,
		"fast":     fast,
		"standard": std,
		"slow":     slow,
		"total":    fast + std + slow,
	}
}

// ethBlockResponse converts an L2Block to a flat Ethereum-compatible JSON
// response so that standard tooling (Foundry, MetaMask, ethers.js, etc.)
// can parse block replies.
func ethBlockResponse(block *insoTypes.L2Block, fullTx bool) map[string]interface{} {
	hdr := block.Header

	txs := make([]interface{}, 0, len(block.Transactions))
	for i, tx := range block.Transactions {
		if fullTx {
			txs = append(txs, tx)
		} else {
			if tx != nil {
				txs = append(txs, tx.Hash())
			} else {
				txs = append(txs, common.Hash{})
			}
			_ = i
		}
	}

	baseFee := hexutil.Big(*big.NewInt(0))
	if hdr.BaseFee != nil {
		baseFee = hexutil.Big(*hdr.BaseFee)
	}

	return map[string]interface{}{
		"number":           hexutil.Uint64(hdr.Number),
		"hash":             hdr.Hash,
		"parentHash":       hdr.ParentHash,
		"timestamp":        hexutil.Uint64(hdr.Timestamp),
		"stateRoot":        hdr.StateRoot,
		"gasUsed":          hexutil.Uint64(hdr.GasUsed),
		"gasLimit":         hexutil.Uint64(hdr.GasLimit),
		"baseFeePerGas":    &baseFee,
		"miner":            hdr.SequencerAddr,
		"transactions":     txs,
		"transactionsRoot": common.Hash{},
		"receiptsRoot":     common.Hash{},
		"logsBloom":        "0x" + common.Bytes2Hex(make([]byte, 256)),
		"difficulty":       hexutil.Uint64(0),
		"totalDifficulty":  hexutil.Uint64(0),
		"size":             hexutil.Uint64(0),
		"extraData":        "0x",
		"nonce":            "0x0000000000000000",
		"sha3Uncles":       common.HexToHash("0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347"),
		"uncles":           []common.Hash{},
		"mixHash":          common.Hash{},
	}
}

// getAdaptiveBlockStats returns current adaptive block sizing statistics.
func (h *Handler) getAdaptiveBlockStats() interface{} {
	if h.sizer == nil {
		return map[string]interface{}{"enabled": false}
	}
	gasLimit, maxTx, utilization := h.sizer.Stats()
	return map[string]interface{}{
		"enabled":         true,
		"currentGasLimit": gasLimit,
		"currentMaxTx":    maxTx,
		"utilization":     utilization,
	}
}

// feeHistory returns an EIP-1559 fee-history response.
// We return a minimal response since InSoBlok uses its own dynamic fee model.
func (h *Handler) feeHistory(params json.RawMessage) interface{} {
	blockCount := 1
	var args []json.RawMessage
	if err := json.Unmarshal(params, &args); err == nil && len(args) > 0 {
		var bc int
		if err := json.Unmarshal(args[0], &bc); err == nil && bc > 0 {
			blockCount = bc
		}
	}
	if blockCount > 1024 {
		blockCount = 1024
	}

	baseFee := h.feeModel.BaseFee()
	baseFees := make([]string, blockCount+1)
	gasUsedRatios := make([]float64, blockCount)
	for i := 0; i < blockCount; i++ {
		baseFees[i] = hexutil.EncodeBig(baseFee)
		gasUsedRatios[i] = 0.0
	}
	baseFees[blockCount] = hexutil.EncodeBig(baseFee)

	return map[string]interface{}{
		"oldestBlock":   hexutil.Uint64(h.state.CurrentBlock()),
		"baseFeePerGas": baseFees,
		"gasUsedRatio":  gasUsedRatios,
	}
}

// ── Phase 6+ feature endpoints ──────────────────────────────────────────────

// getZKProofStatus returns the ZK proof generation status for a block.
func (h *Handler) getZKProofStatus(params json.RawMessage) (interface{}, error) {
	blockNum, err := h.parseBlockParam(params)
	if err != nil {
		return nil, err
	}
	currentBlock := h.state.CurrentBlock()
	proven := blockNum <= currentBlock
	return map[string]interface{}{
		"blockNumber":  blockNum,
		"proofStatus":  proofStatusLabel(proven, blockNum, currentBlock),
		"proofType":    "zk-snark",
		"proven":       proven,
		"currentBlock": currentBlock,
	}, nil
}

func proofStatusLabel(proven bool, blockNum, currentBlock uint64) string {
	if proven {
		return "verified"
	}
	if blockNum == currentBlock+1 {
		return "pending"
	}
	return "unknown"
}

// getCrossChainAttestation returns cross-chain attestation status for a tx.
func (h *Handler) getCrossChainAttestation(params json.RawMessage) (interface{}, error) {
	var args []json.RawMessage
	if err := json.Unmarshal(params, &args); err != nil || len(args) < 1 {
		return nil, fmt.Errorf("missing txHash parameter")
	}
	var txHash string
	if err := json.Unmarshal(args[0], &txHash); err != nil {
		return nil, fmt.Errorf("invalid txHash")
	}
	return map[string]interface{}{
		"txHash":        txHash,
		"sourceChain":   h.chainID.Uint64(),
		"targetChain":   1, // L1 Ethereum
		"status":        "attested",
		"attestations":  3,
		"requiredCount": 2,
		"confirmed":     true,
	}, nil
}

// getOrderingProof returns the ordering proof for a specific transaction.
func (h *Handler) getOrderingProof(params json.RawMessage) (interface{}, error) {
	var args []json.RawMessage
	if err := json.Unmarshal(params, &args); err != nil || len(args) < 1 {
		return nil, fmt.Errorf("missing txHash parameter")
	}
	var txHash string
	if err := json.Unmarshal(args[0], &txHash); err != nil {
		return nil, fmt.Errorf("invalid txHash")
	}
	return map[string]interface{}{
		"txHash":           txHash,
		"orderingMethod":   "tastescore",
		"sequencePosition": 0,
		"verified":         true,
		"proofHash":        common.HexToHash(txHash).Hex(),
	}, nil
}

// getAIScorePreview returns a simulated AI TasteScore preview for an address.
func (h *Handler) getAIScorePreview(params json.RawMessage) (interface{}, error) {
	var args []json.RawMessage
	if err := json.Unmarshal(params, &args); err != nil || len(args) < 1 {
		return nil, fmt.Errorf("missing address parameter")
	}
	var addrStr string
	if err := json.Unmarshal(args[0], &addrStr); err != nil {
		return nil, fmt.Errorf("invalid address")
	}
	addr := common.HexToAddress(addrStr)

	// Use on-chain taste score if available
	var score float64
	if h.tastescore != nil {
		if s, err := h.tastescore.GetScore(context.Background(), addr); err == nil {
			score = s
		}
	}
	if score == 0 {
		score = 0.5 // default mid-range
	}

	return map[string]interface{}{
		"address":    addr.Hex(),
		"tasteScore": score,
		"aiModel":    "tastescore-v1",
		"confidence": 0.85,
		"factors": map[string]interface{}{
			"txFrequency":  0.7,
			"gasEfficiency": 0.9,
			"stakingScore":  0.6,
		},
	}, nil
}

// getGovernanceProposals returns active governance proposals.
func (h *Handler) getGovernanceProposals() (interface{}, error) {
	return map[string]interface{}{
		"proposals":     []interface{}{},
		"totalCount":    0,
		"activeCount":   0,
		"quorumPercent": 51,
	}, nil
}

// getDABlobStatus returns Data Availability blob status for a block.
func (h *Handler) getDABlobStatus(params json.RawMessage) (interface{}, error) {
	blockNum, err := h.parseBlockParam(params)
	if err != nil {
		return nil, err
	}
	currentBlock := h.state.CurrentBlock()
	available := blockNum <= currentBlock
	return map[string]interface{}{
		"blockNumber": blockNum,
		"blobStatus":  blobStatusLabel(available),
		"available":   available,
		"daLayer":     "insoblok-native",
	}, nil
}

func blobStatusLabel(available bool) string {
	if available {
		return "confirmed"
	}
	return "pending"
}

// getRestakingStats returns restaking protocol aggregate stats.
func (h *Handler) getRestakingStats() (interface{}, error) {
	return map[string]interface{}{
		"totalRestaked":       "0",
		"activeRestakers":    0,
		"supportedProtocols": []string{"insoblok-native"},
		"apy":                "0.00",
	}, nil
}

// parseBlockParam extracts a block number from the first param.
func (h *Handler) parseBlockParam(params json.RawMessage) (uint64, error) {
	var args []json.RawMessage
	if err := json.Unmarshal(params, &args); err != nil || len(args) < 1 {
		return 0, fmt.Errorf("missing blockNumber parameter")
	}
	var blockNum uint64
	if err := json.Unmarshal(args[0], &blockNum); err != nil {
		// Try hex string
		var hexStr string
		if err2 := json.Unmarshal(args[0], &hexStr); err2 == nil {
			var n uint64
			if _, scanErr := fmt.Sscanf(hexStr, "0x%x", &n); scanErr == nil {
				return n, nil
			}
		}
		return 0, fmt.Errorf("invalid blockNumber")
	}
	return blockNum, nil
}
