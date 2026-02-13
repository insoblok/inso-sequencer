package producer

import (
	"context"
	"crypto/sha256"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-sequencer/internal/config"
	"github.com/insoblok/inso-sequencer/internal/mempool"
	"github.com/insoblok/inso-sequencer/internal/state"
	insoTypes "github.com/insoblok/inso-sequencer/pkg/types"
)

// Producer is the block production engine. It periodically drains the mempool,
// executes transactions, computes a new state root, and appends a new L2 block.
type Producer struct {
	mu        sync.Mutex
	cfg       *config.SequencerConfig
	mempool   *mempool.Mempool
	state     *state.Manager
	logger    log.Logger
	sequencer common.Address
	cancel    context.CancelFunc
}

// New creates a new block producer.
func New(cfg *config.SequencerConfig, mp *mempool.Mempool, sm *state.Manager, sequencerAddr common.Address) *Producer {
	return &Producer{
		cfg:       cfg,
		mempool:   mp,
		state:     sm,
		logger:    log.New("module", "producer"),
		sequencer: sequencerAddr,
	}
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
func (p *Producer) produceBlock(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Drain transactions from mempool ordered by priority
	txMetas := p.mempool.PopBatch(p.cfg.MaxTxPerBlock, p.cfg.MaxBlockGas)

	// Even if empty, we produce a block (heartbeat)
	blockNum := p.state.CurrentBlock() + 1
	now := uint64(time.Now().Unix())

	// Collect raw transactions
	txs := make([]*types.Transaction, 0, len(txMetas))
	var gasUsed uint64
	for _, meta := range txMetas {
		txs = append(txs, meta.Tx)
		gasUsed += meta.Tx.Gas()
	}

	// Compute state root (simplified: hash of block number + tx hashes)
	stateRoot := p.computeStateRoot(blockNum, txs)

	// Compute block hash
	blockHash := p.computeBlockHash(blockNum, stateRoot, now)

	// Get L1 origin (latest known L1 block)
	l1Origin := p.state.GetL1Origin()

	header := &insoTypes.L2BlockHeader{
		Number:        blockNum,
		Hash:          blockHash,
		ParentHash:    p.state.GetBlockHash(blockNum - 1),
		Timestamp:     now,
		StateRoot:     stateRoot,
		GasUsed:       gasUsed,
		GasLimit:      p.cfg.MaxBlockGas,
		TxCount:       len(txs),
		SequencerAddr: p.sequencer,
		L1Origin:      l1Origin,
		BaseFee:       big.NewInt(1_000_000_000), // 1 gwei fixed
	}

	block := insoTypes.NewL2Block(header, txs)

	// Persist the block
	p.state.AddBlock(block)

	if len(txs) > 0 {
		p.logger.Info("Block produced",
			"number", blockNum,
			"txCount", len(txs),
			"gasUsed", gasUsed,
			"hash", blockHash.Hex()[:10],
		)
	} else {
		p.logger.Debug("Empty block produced", "number", blockNum)
	}
}

// computeStateRoot produces a deterministic (but simplified) state root.
// In a real implementation this would be a Merkle Patricia Trie root.
func (p *Producer) computeStateRoot(blockNum uint64, txs []*types.Transaction) common.Hash {
	h := sha256.New()
	h.Write(new(big.Int).SetUint64(blockNum).Bytes())
	h.Write(p.state.GetLatestStateRoot().Bytes())
	for _, tx := range txs {
		h.Write(tx.Hash().Bytes())
	}
	var root common.Hash
	copy(root[:], h.Sum(nil))
	return root
}

// computeBlockHash produces a deterministic block hash.
func (p *Producer) computeBlockHash(blockNum uint64, stateRoot common.Hash, timestamp uint64) common.Hash {
	h := sha256.New()
	h.Write(new(big.Int).SetUint64(blockNum).Bytes())
	h.Write(stateRoot.Bytes())
	h.Write(new(big.Int).SetUint64(timestamp).Bytes())
	var hash common.Hash
	copy(hash[:], h.Sum(nil))
	return hash
}
