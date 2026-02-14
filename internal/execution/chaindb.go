package execution

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"

	insoTypes "github.com/insoblok/inso-sequencer/pkg/types"
)

// Key prefixes for the chain database.
var (
	prefixBlock      = []byte("b")  // b + blockNum -> L2Block (JSON)
	prefixBlockHash  = []byte("h")  // h + hash -> blockNum (big-endian uint64)
	prefixReceipt    = []byte("r")  // r + txHash -> Receipt (RLP)
	prefixTx         = []byte("t")  // t + txHash -> Transaction (RLP)
	prefixTxBlock    = []byte("tb") // tb + txHash -> blockNum (big-endian uint64)
	prefixBatch      = []byte("bt") // bt + batchIndex -> Batch (JSON)
	keyCurrentBlock  = []byte("current-block")
	keyLatestRoot    = []byte("latest-root")
	keyLatestBatch   = []byte("latest-batch-index")
	keyGenesisRoot   = []byte("genesis-root")
	keyL1Origin      = []byte("l1-origin")
)

// ChainDB provides persistent storage for blocks, transactions, receipts, and batches.
type ChainDB struct {
	mu   sync.RWMutex
	db   ethdb.Database
	logger log.Logger

	// In-memory caches for hot data
	currentBlock uint64
	stateRoot    common.Hash
}

// NewChainDB wraps an ethdb.Database with chain-specific accessors.
func NewChainDB(db ethdb.Database) *ChainDB {
	cdb := &ChainDB{
		db:     db,
		logger: log.New("module", "chaindb"),
	}

	// Load current block number from DB
	if data, err := db.Get(keyCurrentBlock); err == nil && len(data) > 0 {
		cdb.currentBlock = new(big.Int).SetBytes(data).Uint64()
	}

	// Load latest state root
	if data, err := db.Get(keyLatestRoot); err == nil && len(data) == 32 {
		copy(cdb.stateRoot[:], data)
	}

	return cdb
}

// --- Block operations ---

// WriteBlock persists an L2 block and indexes its transactions.
func (c *ChainDB) WriteBlock(block *insoTypes.L2Block) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	batch := c.db.NewBatch()

	// Serialize block
	blockData, err := json.Marshal(block)
	if err != nil {
		return fmt.Errorf("marshal block: %w", err)
	}

	num := block.Header.Number
	numBytes := new(big.Int).SetUint64(num).Bytes()

	// Block by number
	batch.Put(append(prefixBlock, numBytes...), blockData)

	// Block hash -> number index
	batch.Put(append(prefixBlockHash, block.Header.Hash.Bytes()...), numBytes)

	// Index transactions
	for _, tx := range block.Transactions {
		txRLP, err := rlp.EncodeToBytes(tx)
		if err != nil {
			return fmt.Errorf("encode tx %s: %w", tx.Hash().Hex(), err)
		}
		batch.Put(append(prefixTx, tx.Hash().Bytes()...), txRLP)
		batch.Put(append(prefixTxBlock, tx.Hash().Bytes()...), numBytes)
	}

	// Index receipts
	for _, receipt := range block.Receipts {
		rcptRLP, err := rlp.EncodeToBytes(receipt)
		if err != nil {
			return fmt.Errorf("encode receipt %s: %w", receipt.TxHash.Hex(), err)
		}
		batch.Put(append(prefixReceipt, receipt.TxHash.Bytes()...), rcptRLP)
	}

	// Update current block number
	batch.Put(keyCurrentBlock, numBytes)

	// Update state root
	batch.Put(keyLatestRoot, block.Header.StateRoot.Bytes())

	if err := batch.Write(); err != nil {
		return fmt.Errorf("write batch: %w", err)
	}

	// Update in-memory cache
	c.currentBlock = num
	c.stateRoot = block.Header.StateRoot

	return nil
}

// ReadBlock retrieves a block by number.
func (c *ChainDB) ReadBlock(num uint64) (*insoTypes.L2Block, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	numBytes := new(big.Int).SetUint64(num).Bytes()
	data, err := c.db.Get(append(prefixBlock, numBytes...))
	if err != nil {
		return nil, nil // not found
	}

	var block insoTypes.L2Block
	if err := json.Unmarshal(data, &block); err != nil {
		return nil, fmt.Errorf("unmarshal block %d: %w", num, err)
	}
	return &block, nil
}

// ReadBlockByHash retrieves a block by hash.
func (c *ChainDB) ReadBlockByHash(hash common.Hash) (*insoTypes.L2Block, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	numBytes, err := c.db.Get(append(prefixBlockHash, hash.Bytes()...))
	if err != nil {
		return nil, nil
	}

	num := new(big.Int).SetBytes(numBytes).Uint64()
	data, err := c.db.Get(append(prefixBlock, numBytes...))
	if err != nil {
		return nil, fmt.Errorf("block %d data missing for hash %s", num, hash.Hex())
	}

	var block insoTypes.L2Block
	if err := json.Unmarshal(data, &block); err != nil {
		return nil, fmt.Errorf("unmarshal block: %w", err)
	}
	return &block, nil
}

// ReadBlockHash returns the hash of a block by number.
func (c *ChainDB) ReadBlockHash(num uint64) common.Hash {
	block, err := c.ReadBlock(num)
	if err != nil || block == nil {
		return common.Hash{}
	}
	return block.Header.Hash
}

// --- Transaction operations ---

// ReadTransaction retrieves a transaction by hash.
func (c *ChainDB) ReadTransaction(hash common.Hash) (*types.Transaction, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, err := c.db.Get(append(prefixTx, hash.Bytes()...))
	if err != nil {
		return nil, nil
	}

	var tx types.Transaction
	if err := rlp.DecodeBytes(data, &tx); err != nil {
		return nil, fmt.Errorf("decode tx: %w", err)
	}
	return &tx, nil
}

// ReadReceipt retrieves a receipt by tx hash.
func (c *ChainDB) ReadReceipt(txHash common.Hash) (*types.Receipt, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, err := c.db.Get(append(prefixReceipt, txHash.Bytes()...))
	if err != nil {
		return nil, nil
	}

	var receipt types.Receipt
	if err := rlp.DecodeBytes(data, &receipt); err != nil {
		return nil, fmt.Errorf("decode receipt: %w", err)
	}
	return &receipt, nil
}

// ReadTxBlockNumber returns the block number containing a given tx.
func (c *ChainDB) ReadTxBlockNumber(txHash common.Hash) (uint64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, err := c.db.Get(append(prefixTxBlock, txHash.Bytes()...))
	if err != nil {
		return 0, false
	}
	return new(big.Int).SetBytes(data).Uint64(), true
}

// --- Batch operations ---

// WriteBatch persists a batch submission record.
func (c *ChainDB) WriteBatch(batch *insoTypes.Batch) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("marshal batch: %w", err)
	}

	idxBytes := new(big.Int).SetUint64(batch.Index).Bytes()
	dbBatch := c.db.NewBatch()
	dbBatch.Put(append(prefixBatch, idxBytes...), data)
	dbBatch.Put(keyLatestBatch, idxBytes)

	return dbBatch.Write()
}

// ReadBatch retrieves a batch by index.
func (c *ChainDB) ReadBatch(index uint64) (*insoTypes.Batch, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	data, err := c.db.Get(append(prefixBatch, new(big.Int).SetUint64(index).Bytes()...))
	if err != nil {
		return nil, nil
	}

	var batch insoTypes.Batch
	if err := json.Unmarshal(data, &batch); err != nil {
		return nil, fmt.Errorf("unmarshal batch: %w", err)
	}
	return &batch, nil
}

// ReadLatestBatch retrieves the most recent batch.
func (c *ChainDB) ReadLatestBatch() (*insoTypes.Batch, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	idxBytes, err := c.db.Get(keyLatestBatch)
	if err != nil {
		return nil, nil
	}

	data, err := c.db.Get(append(prefixBatch, idxBytes...))
	if err != nil {
		return nil, nil
	}

	var batch insoTypes.Batch
	if err := json.Unmarshal(data, &batch); err != nil {
		return nil, fmt.Errorf("unmarshal batch: %w", err)
	}
	return &batch, nil
}

// --- Meta operations ---

// CurrentBlock returns the latest persisted block number.
func (c *ChainDB) CurrentBlock() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.currentBlock
}

// LatestStateRoot returns the latest persisted state root.
func (c *ChainDB) LatestStateRoot() common.Hash {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stateRoot
}

// WriteGenesisRoot stores the genesis state root.
func (c *ChainDB) WriteGenesisRoot(root common.Hash) error {
	return c.db.Put(keyGenesisRoot, root.Bytes())
}

// ReadGenesisRoot retrieves the genesis state root.
func (c *ChainDB) ReadGenesisRoot() (common.Hash, error) {
	data, err := c.db.Get(keyGenesisRoot)
	if err != nil {
		return common.Hash{}, err
	}
	return common.BytesToHash(data), nil
}

// WriteL1Origin persists the latest L1 origin.
func (c *ChainDB) WriteL1Origin(origin *insoTypes.L1Origin) error {
	data, err := json.Marshal(origin)
	if err != nil {
		return err
	}
	return c.db.Put(keyL1Origin, data)
}

// ReadL1Origin retrieves the latest L1 origin.
func (c *ChainDB) ReadL1Origin() (*insoTypes.L1Origin, error) {
	data, err := c.db.Get(keyL1Origin)
	if err != nil {
		return nil, nil
	}
	var origin insoTypes.L1Origin
	if err := json.Unmarshal(data, &origin); err != nil {
		return nil, err
	}
	return &origin, nil
}
