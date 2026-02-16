package mempool

import (
	"container/heap"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"

	insoTypes "github.com/insoblok/inso-sequencer/pkg/types"
)

// Lane represents a reputation-gated execution lane.
// Higher-tier users get guaranteed block space in faster lanes.
type Lane int

const (
	// LaneFast is for Platinum/Gold tier users — guaranteed 40% of block gas.
	LaneFast Lane = iota
	// LaneStandard is for Silver/Bronze tier users — gets 40% of block gas.
	LaneStandard
	// LaneSlow is for unscored/low-reputation users — gets 20% of block gas.
	LaneSlow
)

// LaneGasAllocation defines the gas budget percentage for each lane (basis points).
const (
	LaneFastGasBps     = 4000 // 40%
	LaneStandardGasBps = 4000 // 40%
	LaneSlowGasBps     = 2000 // 20%
)

// TasteScoreTier thresholds for lane assignment.
const (
	FastLaneMinScore     = 0.65 // Gold+ tier (TasteScore >= 65%)
	StandardLaneMinScore = 0.15 // Bronze+ tier
)

// LanedMempool extends Mempool with reputation-gated execution lanes.
// Transactions are routed into Fast/Standard/Slow lanes based on the
// sender's sovereignty tier. Each lane gets a guaranteed share of block gas,
// preventing high-reputation users from being crowded out and ensuring
// fair access for newcomers.
type LanedMempool struct {
	mu     sync.RWMutex
	fast   txQueue
	std    txQueue
	slow   txQueue
	known  map[common.Hash]Lane
	maxSize int
	tasteScoreWeight float64
	logger log.Logger
}

// NewLanedMempool creates a new mempool with execution lanes.
func NewLanedMempool(tasteScoreWeight float64, maxSize int) *LanedMempool {
	lm := &LanedMempool{
		fast:   make(txQueue, 0, maxSize/3),
		std:    make(txQueue, 0, maxSize/3),
		slow:   make(txQueue, 0, maxSize/3),
		known:  make(map[common.Hash]Lane, maxSize),
		maxSize: maxSize,
		tasteScoreWeight: tasteScoreWeight,
		logger: log.New("module", "laned-mempool"),
	}
	heap.Init(&lm.fast)
	heap.Init(&lm.std)
	heap.Init(&lm.slow)
	return lm
}

// ClassifyLane determines which execution lane a transaction belongs to.
func ClassifyLane(tasteScore float64) Lane {
	switch {
	case tasteScore >= FastLaneMinScore:
		return LaneFast
	case tasteScore >= StandardLaneMinScore:
		return LaneStandard
	default:
		return LaneSlow
	}
}

// Add inserts a transaction into the appropriate lane.
func (lm *LanedMempool) Add(tx *types.Transaction, sender common.Address, tasteScore float64) error {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	txHash := tx.Hash()
	if _, exists := lm.known[txHash]; exists {
		return ErrAlreadyKnown
	}
	if lm.totalLen() >= lm.maxSize {
		return ErrMempoolFull
	}

	meta := &insoTypes.TxMeta{
		Tx:         tx,
		TasteScore: tasteScore,
		ReceivedAt: time.Now(),
		Sender:     sender,
		Nonce:      tx.Nonce(),
		GasPrice:   tx.GasPrice(),
		GasTipCap:  tx.GasTipCap(),
		GasFeeCap:  tx.GasFeeCap(),
	}
	meta.ComputePriority(lm.tasteScoreWeight)

	lane := ClassifyLane(tasteScore)
	switch lane {
	case LaneFast:
		heap.Push(&lm.fast, meta)
	case LaneStandard:
		heap.Push(&lm.std, meta)
	default:
		heap.Push(&lm.slow, meta)
	}
	lm.known[txHash] = lane

	lm.logger.Debug("Transaction added to lane",
		"hash", txHash.Hex(),
		"lane", laneString(lane),
		"tasteScore", tasteScore,
		"priority", meta.Priority,
	)

	return nil
}

// PopBatchLaned drains transactions from all lanes respecting gas allocations.
// Each lane gets its guaranteed gas allocation, and leftover gas is given
// to the next lane (fast → standard → slow).
func (lm *LanedMempool) PopBatchLaned(maxTx int, gasLimit uint64) []*insoTypes.TxMeta {
	lm.mu.Lock()
	defer lm.mu.Unlock()

	fastGas := gasLimit * LaneFastGasBps / 10000
	stdGas := gasLimit * LaneStandardGasBps / 10000
	slowGas := gasLimit * LaneSlowGasBps / 10000

	var batch []*insoTypes.TxMeta
	var totalGas uint64

	// Phase 1: Drain each lane up to its allocation
	fastBatch, fastUsed := lm.drainLane(&lm.fast, maxTx-len(batch), fastGas)
	batch = append(batch, fastBatch...)
	totalGas += fastUsed

	stdBatch, stdUsed := lm.drainLane(&lm.std, maxTx-len(batch), stdGas)
	batch = append(batch, stdBatch...)
	totalGas += stdUsed

	slowBatch, slowUsed := lm.drainLane(&lm.slow, maxTx-len(batch), slowGas)
	batch = append(batch, slowBatch...)
	totalGas += slowUsed

	// Phase 2: Fill remaining gas from any lane (fast first)
	remaining := gasLimit - totalGas
	if remaining > 0 && len(batch) < maxTx {
		extra, _ := lm.drainLane(&lm.fast, maxTx-len(batch), remaining)
		batch = append(batch, extra...)
	}
	if remaining > 0 && len(batch) < maxTx {
		extra, _ := lm.drainLane(&lm.std, maxTx-len(batch), remaining)
		batch = append(batch, extra...)
	}
	if remaining > 0 && len(batch) < maxTx {
		extra, _ := lm.drainLane(&lm.slow, maxTx-len(batch), remaining)
		batch = append(batch, extra...)
	}

	return batch
}

// drainLane pops transactions from a specific lane up to gas and count limits.
func (lm *LanedMempool) drainLane(q *txQueue, maxTx int, gasLimit uint64) ([]*insoTypes.TxMeta, uint64) {
	var batch []*insoTypes.TxMeta
	var gasUsed uint64

	for q.Len() > 0 && len(batch) < maxTx {
		item := heap.Pop(q).(*insoTypes.TxMeta)
		txGas := item.Tx.Gas()
		if gasUsed+txGas > gasLimit {
			heap.Push(q, item) // doesn't fit, put back
			break
		}
		delete(lm.known, item.Tx.Hash())
		batch = append(batch, item)
		gasUsed += txGas
	}

	return batch, gasUsed
}

// Len returns total pending across all lanes.
func (lm *LanedMempool) Len() int {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return lm.totalLen()
}

func (lm *LanedMempool) totalLen() int {
	return lm.fast.Len() + lm.std.Len() + lm.slow.Len()
}

// LaneSizes returns the number of pending transactions in each lane.
func (lm *LanedMempool) LaneSizes() (fast, standard, slow int) {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	return lm.fast.Len(), lm.std.Len(), lm.slow.Len()
}

// Has returns true if the transaction is in any lane.
func (lm *LanedMempool) Has(txHash common.Hash) bool {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	_, ok := lm.known[txHash]
	return ok
}

// Reset clears all lanes.
func (lm *LanedMempool) Reset() {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lm.fast = make(txQueue, 0)
	lm.std = make(txQueue, 0)
	lm.slow = make(txQueue, 0)
	lm.known = make(map[common.Hash]Lane)
	heap.Init(&lm.fast)
	heap.Init(&lm.std)
	heap.Init(&lm.slow)
}

// PopBatch implements TxPool by delegating to PopBatchLaned.
func (lm *LanedMempool) PopBatch(maxTx int, gasLimit uint64) []*insoTypes.TxMeta {
	return lm.PopBatchLaned(maxTx, gasLimit)
}

// Pending returns a snapshot of all pending transactions across all lanes.
func (lm *LanedMempool) Pending() []*insoTypes.TxMeta {
	lm.mu.RLock()
	defer lm.mu.RUnlock()
	all := make([]*insoTypes.TxMeta, 0, lm.totalLen())
	for _, item := range lm.fast {
		all = append(all, item)
	}
	for _, item := range lm.std {
		all = append(all, item)
	}
	for _, item := range lm.slow {
		all = append(all, item)
	}
	return all
}

// Remove discards a transaction from whichever lane it belongs to.
func (lm *LanedMempool) Remove(txHash common.Hash) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	lane, ok := lm.known[txHash]
	if !ok {
		return
	}
	delete(lm.known, txHash)
	var q *txQueue
	switch lane {
	case LaneFast:
		q = &lm.fast
	case LaneStandard:
		q = &lm.std
	default:
		q = &lm.slow
	}
	for i, meta := range *q {
		if meta.Tx.Hash() == txHash {
			heap.Remove(q, i)
			break
		}
	}
}

func laneString(l Lane) string {
	switch l {
	case LaneFast:
		return "fast"
	case LaneStandard:
		return "standard"
	case LaneSlow:
		return "slow"
	default:
		return "unknown"
	}
}
