package batcher

import (
	"context"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-sequencer/internal/config"
	"github.com/insoblok/inso-sequencer/internal/state"
	insoTypes "github.com/insoblok/inso-sequencer/pkg/types"
)

// Batcher submits L2 block batches to L1 at configured intervals.
type Batcher struct {
	mu              sync.Mutex
	cfg             *config.L1Config
	state           *state.Manager
	logger          log.Logger
	lastBatchBlock  uint64
	batchIndex      uint64
	cancel          context.CancelFunc
}

// New creates a new batch submitter.
func New(cfg *config.L1Config, sm *state.Manager) *Batcher {
	return &Batcher{
		cfg:    cfg,
		state:  sm,
		logger: log.New("module", "batcher"),
	}
}

// Start begins the batch submission loop.
func (b *Batcher) Start(ctx context.Context) {
	ctx, b.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(b.cfg.SubmissionInterval)
	defer ticker.Stop()

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
		}
	}
}

// Stop halts the batch submission loop.
func (b *Batcher) Stop() {
	if b.cancel != nil {
		b.cancel()
	}
}

// submitBatch collects new L2 blocks since the last batch and submits them to L1.
func (b *Batcher) submitBatch(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()

	currentBlock := b.state.CurrentBlock()
	if currentBlock <= b.lastBatchBlock {
		return // no new blocks
	}

	startBlock := b.lastBatchBlock + 1
	endBlock := currentBlock
	blockCount := int(endBlock - startBlock + 1)

	b.logger.Info("Submitting batch to L1",
		"batchIndex", b.batchIndex+1,
		"startBlock", startBlock,
		"endBlock", endBlock,
		"blockCount", blockCount,
	)

	// In a real implementation, this would:
	// 1. RLP-encode the block range
	// 2. Compress with zlib/brotli
	// 3. Submit as calldata to L1 BatchInbox contract
	// 4. Or use EIP-4844 blobs for cheaper data availability

	// Simulate L1 submission
	txHash := b.state.SimulateL1Submission(startBlock, endBlock)

	batch := &insoTypes.Batch{
		Index:      b.batchIndex + 1,
		StartBlock: startBlock,
		EndBlock:   endBlock,
		BlockCount: blockCount,
		L1TxHash:   txHash,
		Timestamp:  time.Now(),
		SizeBytes:  uint64(blockCount) * 1024, // estimated
		Status:     insoTypes.BatchSubmitted,
	}

	b.state.AddBatch(batch)
	b.lastBatchBlock = endBlock
	b.batchIndex++

	b.logger.Info("Batch submitted to L1",
		"batchIndex", batch.Index,
		"l1TxHash", txHash.Hex()[:10],
		"sizeBytes", batch.SizeBytes,
	)
}
