package producer

import (
	"context"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-sequencer/internal/config"
	"github.com/insoblok/inso-sequencer/internal/fees"
	"github.com/insoblok/inso-sequencer/internal/mempool"
	"github.com/insoblok/inso-sequencer/internal/state"
)

// Producer is the block production engine. It periodically drains the mempool,
// executes transactions through the EVM, and persists new L2 blocks.
// Phase 4: uses DynamicFeeModel for EIP-1559–style base fee adjustments
// with sovereignty-tier discounts.
type Producer struct {
	mu        sync.Mutex
	cfg       *config.SequencerConfig
	mempool   *mempool.Mempool
	state     *state.Manager
	feeModel  *fees.DynamicFeeModel
	logger    log.Logger
	sequencer common.Address
	cancel    context.CancelFunc
}

// New creates a new block producer.
func New(cfg *config.SequencerConfig, mp *mempool.Mempool, sm *state.Manager, sequencerAddr common.Address, fm *fees.DynamicFeeModel) *Producer {
	return &Producer{
		cfg:       cfg,
		mempool:   mp,
		state:     sm,
		feeModel:  fm,
		logger:    log.New("module", "producer"),
		sequencer: sequencerAddr,
	}
}

// FeeModel returns the dynamic fee model for RPC/external access.
func (p *Producer) FeeModel() *fees.DynamicFeeModel {
	return p.feeModel
}

// Start begins the block production loop. Runs until the context is cancelled.
func (p *Producer) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)
	ticker := time.NewTicker(p.cfg.BlockTime)
	defer ticker.Stop()

	p.logger.Info("Block producer started",
		"blockTime", p.cfg.BlockTime,
		"maxBlockGas", p.cfg.MaxBlockGas,
		"maxTxPerBlock", p.cfg.MaxTxPerBlock,
	)

	for {
		select {
		case <-ctx.Done():
			p.logger.Info("Block producer stopped")
			return
		case <-ticker.C:
			p.produceBlock(ctx)
		}
	}
}

// Stop halts the block production loop.
func (p *Producer) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
}

// produceBlock creates a single L2 block from the current mempool.
// It drains pending transactions, executes them through the EVM,
// computes a real Merkle Patricia Trie state root, and persists everything.
func (p *Producer) produceBlock(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Drain transactions from mempool ordered by priority
	txMetas := p.mempool.PopBatch(p.cfg.MaxTxPerBlock, p.cfg.MaxBlockGas)

	blockNum := p.state.CurrentBlock() + 1
	now := uint64(time.Now().Unix())
	parentHash := p.state.GetBlockHash(blockNum - 1)
	baseFee := p.feeModel.BaseFee() // Phase 4: dynamic base fee

	// Collect raw transactions
	txs := make([]*types.Transaction, 0, len(txMetas))
	for _, meta := range txMetas {
		txs = append(txs, meta.Tx)
	}

	// Execute transactions through EVM and commit state
	block, skipped, err := p.state.ExecuteAndCommitBlock(txs, blockNum, now, parentHash, baseFee)
	if err != nil {
		p.logger.Error("Failed to produce block", "number", blockNum, "err", err)
		// Return skipped txs to mempool
		for _, tx := range txs {
			// Best-effort: re-add all txs if block production failed completely
			_ = p.mempool.Add(tx, common.Address{}, 0)
		}
		return
	}

	// Return skipped txs to mempool for next block
	for _, tx := range skipped {
		for _, meta := range txMetas {
			if meta.Tx.Hash() == tx.Hash() {
				_ = p.mempool.Add(tx, meta.Sender, meta.TasteScore)
				break
			}
		}
	}

	executedCount := block.Header.TxCount
	if executedCount > 0 {
		p.logger.Info("Block produced",
			"number", blockNum,
			"txCount", executedCount,
			"gasUsed", block.Header.GasUsed,
			"stateRoot", block.Header.StateRoot.Hex()[:10],
			"hash", block.Header.Hash.Hex()[:10],
			"skipped", len(skipped),
			"baseFee", baseFee,
		)

		// Log receipt details for debugging
		for _, receipt := range block.Receipts {
			status := "success"
			if receipt.Status == types.ReceiptStatusFailed {
				status = "failed"
			}
			p.logger.Debug("Transaction receipt",
				"txHash", receipt.TxHash.Hex()[:10],
				"status", status,
				"gasUsed", receipt.GasUsed,
				"logs", len(receipt.Logs),
			)
		}
	} else {
		p.logger.Debug("Empty block produced",
			"number", blockNum,
			"stateRoot", block.Header.StateRoot.Hex()[:10],
		)
	}

	// Phase 4: adjust base fee based on block gas utilization
	p.feeModel.AdjustAfterBlock(block.Header.GasUsed, p.cfg.MaxBlockGas)
}
