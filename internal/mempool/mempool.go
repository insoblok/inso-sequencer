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

// Mempool is a thread-safe transaction pool that orders transactions
// using a combination of gas price and TasteScore (fair FIFO).
type Mempool struct {
	mu               sync.RWMutex
	pending          txQueue
	known            map[common.Hash]struct{}
	senderNonces     map[common.Address]uint64
	tasteScoreWeight float64
	maxSize          int
	logger           log.Logger
}

// New creates a new Mempool.
func New(tasteScoreWeight float64, maxSize int) *Mempool {
	m := &Mempool{
		pending:          make(txQueue, 0, maxSize),
		known:            make(map[common.Hash]struct{}, maxSize),
		senderNonces:     make(map[common.Address]uint64),
		tasteScoreWeight: tasteScoreWeight,
		maxSize:          maxSize,
		logger:           log.New("module", "mempool"),
	}
	heap.Init(&m.pending)
	return m
}

// Add inserts a transaction into the mempool with its TasteScore metadata.
func (m *Mempool) Add(tx *types.Transaction, sender common.Address, tasteScore float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	txHash := tx.Hash()

	// Reject duplicates
	if _, exists := m.known[txHash]; exists {
		return ErrAlreadyKnown
	}

	// Reject if full
	if len(m.pending) >= m.maxSize {
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
	meta.ComputePriority(m.tasteScoreWeight)

	heap.Push(&m.pending, meta)
	m.known[txHash] = struct{}{}

	m.logger.Debug("Transaction added to mempool",
		"hash", txHash.Hex(),
		"sender", sender.Hex(),
		"tasteScore", tasteScore,
		"priority", meta.Priority,
		"poolSize", len(m.pending),
	)

	return nil
}

// Pop removes and returns the highest-priority transaction.
func (m *Mempool) Pop() *insoTypes.TxMeta {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.pending) == 0 {
		return nil
	}

	item := heap.Pop(&m.pending).(*insoTypes.TxMeta)
	delete(m.known, item.Tx.Hash())
	return item
}

// Peek returns the highest-priority transaction without removing it.
func (m *Mempool) Peek() *insoTypes.TxMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.pending) == 0 {
		return nil
	}
	return m.pending[0]
}

// PopBatch removes up to `max` transactions ordered by priority,
// respecting the gas limit.
func (m *Mempool) PopBatch(maxTx int, gasLimit uint64) []*insoTypes.TxMeta {
	m.mu.Lock()
	defer m.mu.Unlock()

	var (
		batch    []*insoTypes.TxMeta
		gasTotal uint64
	)

	for len(m.pending) > 0 && len(batch) < maxTx {
		item := heap.Pop(&m.pending).(*insoTypes.TxMeta)

		txGas := item.Tx.Gas()
		if gasTotal+txGas > gasLimit {
			// Put it back — doesn't fit in this block
			heap.Push(&m.pending, item)
			break
		}

		delete(m.known, item.Tx.Hash())
		batch = append(batch, item)
		gasTotal += txGas
	}

	return batch
}

// Remove discards a transaction from the pool (e.g., after inclusion).
func (m *Mempool) Remove(txHash common.Hash) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.known, txHash)

	for i, meta := range m.pending {
		if meta.Tx.Hash() == txHash {
			heap.Remove(&m.pending, i)
			break
		}
	}
}

// Has returns true if the transaction is in the pool.
func (m *Mempool) Has(txHash common.Hash) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.known[txHash]
	return ok
}

// Len returns the number of pending transactions.
func (m *Mempool) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.pending)
}

// Pending returns a snapshot of all pending transactions (highest priority first).
func (m *Mempool) Pending() []*insoTypes.TxMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()

	snapshot := make([]*insoTypes.TxMeta, len(m.pending))
	copy(snapshot, m.pending)
	return snapshot
}

// Reset clears the mempool.
func (m *Mempool) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.pending = make(txQueue, 0, m.maxSize)
	m.known = make(map[common.Hash]struct{}, m.maxSize)
	heap.Init(&m.pending)
}
