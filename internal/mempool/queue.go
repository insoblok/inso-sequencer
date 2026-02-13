package mempool

import (
	insoTypes "github.com/insoblok/inso-sequencer/pkg/types"
)

// txQueue implements heap.Interface for priority-ordered transactions.
// Higher priority = popped first (max-heap).
type txQueue []*insoTypes.TxMeta

func (q txQueue) Len() int { return len(q) }

func (q txQueue) Less(i, j int) bool {
	// Higher priority first
	if q[i].Priority != q[j].Priority {
		return q[i].Priority > q[j].Priority
	}
	// FIFO tie-break: earlier received = higher priority
	return q[i].ReceivedAt.Before(q[j].ReceivedAt)
}

func (q txQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }

func (q *txQueue) Push(x interface{}) {
	*q = append(*q, x.(*insoTypes.TxMeta))
}

func (q *txQueue) Pop() interface{} {
	old := *q
	n := len(old)
	item := old[n-1]
	old[n-1] = nil // avoid memory leak
	*q = old[:n-1]
	return item
}
