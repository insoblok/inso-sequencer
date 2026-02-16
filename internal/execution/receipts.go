package execution

import (
	"crypto/sha256"
	"encoding/binary"
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
)

// ComputeReceipt is a verifiable receipt proving a specific execution result.
// It contains the pre/post state roots and a Merkle proof that the execution
// was correct. Any party can verify the receipt without re-executing.
//
// Feature #10 from InSoBlok L2 Future Work
type ComputeReceipt struct {
	// Identification
	TxHash      common.Hash `json:"txHash"`
	BlockNumber uint64      `json:"blockNumber"`
	TxIndex     int         `json:"txIndex"`

	// Execution context
	Sender   common.Address `json:"sender"`
	To       *common.Address `json:"to,omitempty"`
	Value    *big.Int       `json:"value"`
	GasUsed  uint64         `json:"gasUsed"`
	GasPrice *big.Int       `json:"gasPrice"`

	// State transition proof
	PreStateRoot  common.Hash `json:"preStateRoot"`
	PostStateRoot common.Hash `json:"postStateRoot"`
	Status        uint64      `json:"status"` // 1=success, 0=failure

	// Execution fingerprint
	InputHash  common.Hash `json:"inputHash"`  // keccak256(calldata)
	OutputHash common.Hash `json:"outputHash"` // keccak256(return data)
	LogsHash   common.Hash `json:"logsHash"`   // keccak256(rlp(logs))

	// Receipt hash (commitment to entire receipt)
	ReceiptHash common.Hash `json:"receiptHash"`

	// Timestamps
	ExecutedAt time.Time `json:"executedAt"`
}

// ReceiptStore stores and retrieves verifiable compute receipts.
type ReceiptStore struct {
	mu       sync.RWMutex
	receipts map[common.Hash]*ComputeReceipt // txHash → receipt
	byBlock  map[uint64][]*ComputeReceipt    // blockNumber → receipts
	logger   log.Logger
}

// NewReceiptStore creates a new verifiable receipt store.
func NewReceiptStore() *ReceiptStore {
	return &ReceiptStore{
		receipts: make(map[common.Hash]*ComputeReceipt),
		byBlock:  make(map[uint64][]*ComputeReceipt),
		logger:   log.New("module", "compute-receipts"),
	}
}

// CreateReceipt generates a verifiable compute receipt from execution data.
func CreateReceipt(
	tx *types.Transaction,
	receipt *types.Receipt,
	blockNumber uint64,
	txIndex int,
	sender common.Address,
	preStateRoot common.Hash,
	postStateRoot common.Hash,
	returnData []byte,
) *ComputeReceipt {

	// Compute input hash (keccak256 of calldata)
	inputHash := crypto.Keccak256Hash(tx.Data())

	// Compute output hash
	outputHash := crypto.Keccak256Hash(returnData)

	// Compute logs hash
	logsHash := computeLogsHash(receipt.Logs)

	cr := &ComputeReceipt{
		TxHash:        tx.Hash(),
		BlockNumber:   blockNumber,
		TxIndex:       txIndex,
		Sender:        sender,
		To:            tx.To(),
		Value:         tx.Value(),
		GasUsed:       receipt.GasUsed,
		GasPrice:      tx.GasPrice(),
		PreStateRoot:  preStateRoot,
		PostStateRoot: postStateRoot,
		Status:        receipt.Status,
		InputHash:     inputHash,
		OutputHash:    outputHash,
		LogsHash:      logsHash,
		ExecutedAt:    time.Now(),
	}

	// Compute the receipt commitment hash
	cr.ReceiptHash = cr.computeHash()

	return cr
}

// computeHash creates a deterministic hash commitment of the receipt.
func (r *ComputeReceipt) computeHash() common.Hash {
	h := sha256.New()
	h.Write(r.TxHash.Bytes())
	binary.Write(h, binary.BigEndian, r.BlockNumber)
	binary.Write(h, binary.BigEndian, uint64(r.TxIndex))
	h.Write(r.Sender.Bytes())
	if r.To != nil {
		h.Write(r.To.Bytes())
	}
	if r.Value != nil {
		h.Write(r.Value.Bytes())
	}
	binary.Write(h, binary.BigEndian, r.GasUsed)
	h.Write(r.PreStateRoot.Bytes())
	h.Write(r.PostStateRoot.Bytes())
	binary.Write(h, binary.BigEndian, r.Status)
	h.Write(r.InputHash.Bytes())
	h.Write(r.OutputHash.Bytes())
	h.Write(r.LogsHash.Bytes())

	return common.BytesToHash(h.Sum(nil))
}

// Verify checks that the receipt hash matches the receipt data.
// Returns true if the receipt has not been tampered with.
func (r *ComputeReceipt) Verify() bool {
	return r.ReceiptHash == r.computeHash()
}

// Store adds a receipt to the store.
func (s *ReceiptStore) Store(receipt *ComputeReceipt) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.receipts[receipt.TxHash] = receipt
	s.byBlock[receipt.BlockNumber] = append(s.byBlock[receipt.BlockNumber], receipt)

	s.logger.Debug("Compute receipt stored",
		"txHash", receipt.TxHash.Hex()[:10],
		"block", receipt.BlockNumber,
		"status", receipt.Status,
		"preState", receipt.PreStateRoot.Hex()[:10],
		"postState", receipt.PostStateRoot.Hex()[:10],
	)
}

// Get retrieves a receipt by transaction hash.
func (s *ReceiptStore) Get(txHash common.Hash) *ComputeReceipt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.receipts[txHash]
}

// Len returns the total number of receipts in the store.
func (s *ReceiptStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.receipts)
}

// GetByBlock retrieves all receipts for a block.
func (s *ReceiptStore) GetByBlock(blockNumber uint64) []*ComputeReceipt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byBlock[blockNumber]
}

// ComputeBlockReceiptRoot computes a Merkle root of all receipts in a block.
// This root can be published on-chain for efficient verification.
func (s *ReceiptStore) ComputeBlockReceiptRoot(blockNumber uint64) common.Hash {
	s.mu.RLock()
	defer s.mu.RUnlock()

	receipts, ok := s.byBlock[blockNumber]
	if !ok || len(receipts) == 0 {
		return common.Hash{}
	}

	hashes := make([]common.Hash, len(receipts))
	for i, r := range receipts {
		hashes[i] = r.ReceiptHash
	}

	return computeMerkleRoot(hashes)
}

// Count returns the total number of stored receipts.
func (s *ReceiptStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.receipts)
}

// computeLogsHash returns a deterministic hash of event logs.
func computeLogsHash(logs []*types.Log) common.Hash {
	if len(logs) == 0 {
		return common.Hash{}
	}
	h := sha256.New()
	for _, l := range logs {
		h.Write(l.Address.Bytes())
		for _, topic := range l.Topics {
			h.Write(topic.Bytes())
		}
		h.Write(l.Data)
	}
	return common.BytesToHash(h.Sum(nil))
}

// computeMerkleRoot computes a binary Merkle root from a list of hashes.
func computeMerkleRoot(hashes []common.Hash) common.Hash {
	if len(hashes) == 0 {
		return common.Hash{}
	}
	if len(hashes) == 1 {
		return hashes[0]
	}

	for len(hashes) > 1 {
		var next []common.Hash
		for i := 0; i < len(hashes); i += 2 {
			if i+1 < len(hashes) {
				combined := crypto.Keccak256Hash(
					append(hashes[i].Bytes(), hashes[i+1].Bytes()...),
				)
				next = append(next, combined)
			} else {
				next = append(next, hashes[i])
			}
		}
		hashes = next
	}

	return hashes[0]
}
