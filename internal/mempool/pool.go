package mempool

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"

	insoTypes "github.com/insoblok/inso-sequencer/pkg/types"
)

// TxPool is the interface for transaction pool implementations.
// Both Mempool (basic) and LanedMempool (reputation-gated) implement this.
type TxPool interface {
	// Add inserts a transaction into the pool.
	Add(tx *types.Transaction, sender common.Address, tasteScore float64) error

	// PopBatch removes up to maxTx transactions respecting gasLimit.
	PopBatch(maxTx int, gasLimit uint64) []*insoTypes.TxMeta

	// Len returns the number of pending transactions.
	Len() int

	// Pending returns a snapshot of all pending transactions.
	Pending() []*insoTypes.TxMeta

	// Has returns true if the transaction is in the pool.
	Has(txHash common.Hash) bool
}
