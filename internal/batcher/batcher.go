package batcher

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"io"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"

	"github.com/insoblok/inso-sequencer/internal/config"
	"github.com/insoblok/inso-sequencer/internal/metrics"
	"github.com/insoblok/inso-sequencer/internal/state"
	insoTypes "github.com/insoblok/inso-sequencer/pkg/types"
)

// ── Batch frame types (RLP-encodable) ────────────────────────────────────────

// BatchFrame is the top-level RLP-encodable batch of L2 blocks.
type BatchFrame struct {
	Version    uint8
	ChainID    uint64
	StartBlock uint64
	EndBlock   uint64
	Timestamp  uint64
	Blocks     []BlockData
}

// BlockData holds per-block data within a batch.
type BlockData struct {
	Number     uint64
	Hash       common.Hash
	ParentHash common.Hash
	Timestamp  uint64
	StateRoot  common.Hash
	GasUsed    uint64
	TxCount    uint64
	TxData     [][]byte // raw RLP-encoded transactions
}

// ── L1 client interface ──────────────────────────────────────────────────────

// L1Client abstracts L1 chain interaction for batch submission.
type L1Client interface {
	// SendBatchTx submits compressed batch calldata to the L1 BatchInbox.
	SendBatchTx(ctx context.Context, data []byte) (common.Hash, error)

	// LatestL1Block returns the latest L1 block number and hash.
	LatestL1Block(ctx context.Context) (uint64, common.Hash, error)

	// IsConfirmed returns true when an L1 tx has >= N confirmations.
	IsConfirmed(ctx context.Context, txHash common.Hash, confirmations uint64) (bool, error)
}

// SimulatedL1Client is a devnet stub that logs submissions locally.
type SimulatedL1Client struct {
	mu       sync.Mutex
	blockNum uint64
	logger   log.Logger
}

// NewSimulatedL1Client creates a simulated L1 client for devnet usage.
func NewSimulatedL1Client() *SimulatedL1Client {
	return &SimulatedL1Client{
		blockNum: 1_000,
		logger:   log.New("module", "l1-sim"),
	}
}

func (s *SimulatedL1Client) SendBatchTx(_ context.Context, data []byte) (common.Hash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blockNum++
	// deterministic hash from data length + block number
	hash := common.BigToHash(new(big.Int).Add(
		new(big.Int).SetUint64(s.blockNum),
		new(big.Int).SetInt64(int64(len(data))),
	))
	s.logger.Info("Simulated L1 batch submission",
		"dataSize", len(data),
		"l1TxHash", hash.Hex()[:16],
		"l1Block", s.blockNum,
	)
	return hash, nil
}

func (s *SimulatedL1Client) LatestL1Block(_ context.Context) (uint64, common.Hash, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blockNum++
	return s.blockNum, common.BigToHash(new(big.Int).SetUint64(s.blockNum)), nil
}

func (s *SimulatedL1Client) IsConfirmed(_ context.Context, _ common.Hash, _ uint64) (bool, error) {
	return true, nil // always confirmed in simulation
}

// ── Batcher ──────────────────────────────────────────────────────────────────

// Batcher submits L2 block batches to L1 at configured intervals.
// Phase 3: real RLP encoding, zlib compression, L1 origin tracking.
type Batcher struct {
	mu             sync.Mutex
	cfg            *config.L1Config
	state          *state.Manager
	l1Client       L1Client
	metrics        *metrics.Metrics
	logger         log.Logger
	lastBatchBlock uint64
	batchIndex     uint64
	chainID        uint64
	cancel         context.CancelFunc

	// Track pending L1 confirmations
	pendingBatches map[common.Hash]*insoTypes.Batch
}

// New creates a new batch submitter.
func New(cfg *config.L1Config, sm *state.Manager, l1Client L1Client, chainID uint64) *Batcher {
	return &Batcher{
		cfg:            cfg,
		state:          sm,
		l1Client:       l1Client,
		logger:         log.New("module", "batcher"),
		chainID:        chainID,
		pendingBatches: make(map[common.Hash]*insoTypes.Batch),
	}
}

// SetMetrics attaches the Prometheus metrics instance.
func (b *Batcher) SetMetrics(m *metrics.Metrics) { b.metrics = m }

// Start begins the batch submission loop.
func (b *Batcher) Start(ctx context.Context) {
	ctx, b.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(b.cfg.SubmissionInterval)
	defer ticker.Stop()

	confirmTicker := time.NewTicker(b.cfg.SubmissionInterval * 2)
	defer confirmTicker.Stop()

	b.logger.Info("Batch submitter started",
		"interval", b.cfg.SubmissionInterval,
		"l1Rpc", b.cfg.RPCURL,
	)

	for {
		select {
		case <-ctx.Done():
			b.logger.Info("Batch submitter stopped")
			return
		case <-ticker.C:
			b.submitBatch(ctx)
		case <-confirmTicker.C:
			b.checkConfirmations(ctx)
		}
	}
}

// Stop halts the batch submission loop.
func (b *Batcher) Stop() {
	if b.cancel != nil {
		b.cancel()
	}
}

// submitBatch collects new L2 blocks, RLP-encodes, compresses, and submits to L1.
func (b *Batcher) submitBatch(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()

	currentBlock := b.state.CurrentBlock()
	if currentBlock <= b.lastBatchBlock {
		return // no new blocks
	}

	startBlock := b.lastBatchBlock + 1
	endBlock := currentBlock

	// ── Build batch frame from L2 blocks ──
	frame, err := b.buildBatchFrame(startBlock, endBlock)
	if err != nil {
		b.logger.Error("Failed to build batch frame", "err", err)
		return
	}

	// ── RLP-encode ──
	encoded, err := rlp.EncodeToBytes(frame)
	if err != nil {
		b.logger.Error("Failed to RLP-encode batch", "err", err)
		return
	}

	// ── Zlib compress ──
	compressed, err := compressData(encoded)
	if err != nil {
		b.logger.Error("Failed to compress batch", "err", err)
		return
	}

	ratio := float64(len(encoded)) / float64(max(1, len(compressed)))
	blockCount := int(endBlock - startBlock + 1)

	b.logger.Info("Submitting batch to L1",
		"batchIndex", b.batchIndex+1,
		"startBlock", startBlock,
		"endBlock", endBlock,
		"blockCount", blockCount,
		"rawSize", len(encoded),
		"compressedSize", len(compressed),
		"compressionRatio", fmt.Sprintf("%.1fx", ratio),
	)

	// ── Update L1 origin ──
	l1Num, l1Hash, err := b.l1Client.LatestL1Block(ctx)
	if err == nil {
		b.state.SetL1Origin(insoTypes.L1Origin{
			BlockNumber: l1Num,
			BlockHash:   l1Hash,
			Timestamp:   uint64(time.Now().Unix()),
		})
	}

	// ── Submit to L1 ──
	txHash, err := b.l1Client.SendBatchTx(ctx, compressed)
	if err != nil {
		b.logger.Error("Failed to submit batch to L1", "err", err)
		return
	}

	batch := &insoTypes.Batch{
		Index:      b.batchIndex + 1,
		StartBlock: startBlock,
		EndBlock:   endBlock,
		BlockCount: blockCount,
		L1TxHash:   txHash,
		Timestamp:  time.Now(),
		SizeBytes:  uint64(len(compressed)),
		Status:     insoTypes.BatchSubmitted,
	}

	b.state.AddBatch(batch)
	b.pendingBatches[txHash] = batch
	b.lastBatchBlock = endBlock
	b.batchIndex++

	b.logger.Info("Batch submitted to L1",
		"batchIndex", batch.Index,
		"l1TxHash", txHash.Hex()[:16],
		"sizeBytes", batch.SizeBytes,
	)

	// Update Prometheus metrics
	if b.metrics != nil {
		b.metrics.BatchesSubmitted.Add(1)
		b.metrics.LastBatchSize.Store(int64(batch.SizeBytes))
	}
}

// buildBatchFrame collects block data from the state manager into an RLP frame.
func (b *Batcher) buildBatchFrame(startBlock, endBlock uint64) (*BatchFrame, error) {
	frame := &BatchFrame{
		Version:    1,
		ChainID:    b.chainID,
		StartBlock: startBlock,
		EndBlock:   endBlock,
		Timestamp:  uint64(time.Now().Unix()),
		Blocks:     make([]BlockData, 0, endBlock-startBlock+1),
	}

	for num := startBlock; num <= endBlock; num++ {
		block := b.state.GetBlock(num)
		if block == nil {
			return nil, fmt.Errorf("block %d not found in chain db", num)
		}

		bd := BlockData{
			Number:     block.Header.Number,
			Hash:       block.Header.Hash,
			ParentHash: block.Header.ParentHash,
			Timestamp:  block.Header.Timestamp,
			StateRoot:  block.Header.StateRoot,
			GasUsed:    block.Header.GasUsed,
			TxCount:    uint64(block.Header.TxCount),
		}

		// RLP-encode each transaction into the frame
		for _, tx := range block.Transactions {
			txRLP, err := rlp.EncodeToBytes(tx)
			if err != nil {
				return nil, fmt.Errorf("encode tx in block %d: %w", num, err)
			}
			bd.TxData = append(bd.TxData, txRLP)
		}

		frame.Blocks = append(frame.Blocks, bd)
	}

	return frame, nil
}

// checkConfirmations polls pending L1 batch txs for confirmation.
func (b *Batcher) checkConfirmations(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for txHash, batch := range b.pendingBatches {
		confirmed, err := b.l1Client.IsConfirmed(ctx, txHash, 6)
		if err != nil {
			b.logger.Debug("Confirmation check failed", "txHash", txHash.Hex()[:16], "err", err)
			continue
		}
		if confirmed {
			batch.Status = insoTypes.BatchConfirmed
			delete(b.pendingBatches, txHash)
			b.logger.Info("Batch confirmed on L1",
				"batchIndex", batch.Index,
				"l1TxHash", txHash.Hex()[:16],
			)
		}
	}
}

// ── Compression helpers ──────────────────────────────────────────────────────

func compressData(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := zlib.NewWriterLevel(&buf, zlib.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecompressData decompresses zlib-compressed batch data (used by validators).
func DecompressData(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
