package types

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// L2BlockHeader represents an InSoBlok L2 block header.
type L2BlockHeader struct {
	Number       uint64      `json:"number"`
	Hash         common.Hash `json:"hash"`
	ParentHash   common.Hash `json:"parentHash"`
	Timestamp    uint64      `json:"timestamp"`
	StateRoot    common.Hash `json:"stateRoot"`
	GasUsed      uint64      `json:"gasUsed"`
	GasLimit     uint64      `json:"gasLimit"`
	TxCount      int         `json:"txCount"`
	SequencerAddr common.Address `json:"sequencer"`
	L1Origin     L1Origin    `json:"l1Origin"`
	BaseFee      *big.Int    `json:"baseFee"`
}

// L1Origin references the L1 block this L2 block derives from.
type L1Origin struct {
	BlockNumber uint64      `json:"blockNumber"`
	BlockHash   common.Hash `json:"blockHash"`
	Timestamp   uint64      `json:"timestamp"`
}

// L2Block is a full L2 block with transactions and receipts.
type L2Block struct {
	Header       *L2BlockHeader       `json:"header"`
	Transactions []*types.Transaction `json:"transactions"`
	Receipts     []*types.Receipt     `json:"receipts,omitempty"`
}

// NewL2Block creates a new L2Block.
func NewL2Block(header *L2BlockHeader, txs []*types.Transaction) *L2Block {
	return &L2Block{
		Header:       header,
		Transactions: txs,
	}
}

// Batch represents a set of L2 blocks submitted to L1.
type Batch struct {
	Index       uint64      `json:"index"`
	StartBlock  uint64      `json:"startBlock"`
	EndBlock    uint64      `json:"endBlock"`
	BlockCount  int         `json:"blockCount"`
	L1TxHash    common.Hash `json:"l1TxHash"`
	Timestamp   time.Time   `json:"timestamp"`
	SizeBytes   uint64      `json:"sizeBytes"`
	Status      BatchStatus `json:"status"`
}

// BatchStatus represents the submission state of a batch.
type BatchStatus int

const (
	BatchPending   BatchStatus = iota
	BatchSubmitted
	BatchConfirmed
	BatchFinalized
	BatchFailed
)

func (s BatchStatus) String() string {
	switch s {
	case BatchPending:
		return "pending"
	case BatchSubmitted:
		return "submitted"
	case BatchConfirmed:
		return "confirmed"
	case BatchFinalized:
		return "finalized"
	case BatchFailed:
		return "failed"
	default:
		return "unknown"
	}
}
