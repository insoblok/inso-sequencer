package state

import (
	"crypto/sha256"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"

	insoTypes "github.com/insoblok/inso-sequencer/pkg/types"
)

// Manager is the in-memory state manager for the L2 chain.
// In production, this would be backed by a persistent database (LevelDB / Pebble).
type Manager struct {
	mu sync.RWMutex

	// Chain state
	blocks       map[uint64]*insoTypes.L2Block
	blockHashes  map[common.Hash]uint64
	currentBlock uint64
	stateRoot    common.Hash

	// Transaction index
	txIndex      map[common.Hash]*types.Transaction
	txReceipts   map[common.Hash]*types.Receipt
	txBlockIndex map[common.Hash]uint64

	// Account state (simplified — balances & nonces)
	balances map[common.Address]*big.Int
	nonces   map[common.Address]uint64

	// Batch tracking
	batches     []*insoTypes.Batch
	latestBatch *insoTypes.Batch

	// L1 origin
	l1Origin insoTypes.L1Origin

	// Sequencer status
	isSequencing  bool
	sequencerAddr common.Address
	chainID       uint64

	logger log.Logger
}

// NewManager creates a new state manager with genesis state.
func NewManager(chainID uint64, sequencerAddr common.Address) *Manager {
	m := &Manager{
		blocks:       make(map[uint64]*insoTypes.L2Block),
		blockHashes:  make(map[common.Hash]uint64),
		txIndex:      make(map[common.Hash]*types.Transaction),
		txReceipts:   make(map[common.Hash]*types.Receipt),
		txBlockIndex: make(map[common.Hash]uint64),
		balances:     make(map[common.Address]*big.Int),
		nonces:       make(map[common.Address]uint64),
		chainID:      chainID,
		sequencerAddr: sequencerAddr,
		isSequencing: true,
		logger:       log.New("module", "state"),
	}

	// Initialize genesis state root
	h := sha256.New()
	h.Write([]byte("insoblok-genesis"))
	h.Write(new(big.Int).SetUint64(chainID).Bytes())
	copy(m.stateRoot[:], h.Sum(nil))

	// Pre-fund genesis accounts (matching devnet genesis)
	genesisAccounts := map[common.Address]*big.Int{
		common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"): parseEther("10000"), // deployer
		common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8"): parseEther("10000"), // user1
		common.HexToAddress("0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC"): parseEther("10000"), // user2
		common.HexToAddress("0x90F79bf6EB2c4f870365E785982E1f101E93b906"): parseEther("100000"), // validator
	}

	for addr, balance := range genesisAccounts {
		m.balances[addr] = balance
	}

	m.logger.Info("State manager initialized",
		"chainID", chainID,
		"genesisAccounts", len(genesisAccounts),
	)

	return m
}

func parseEther(amount string) *big.Int {
	val, _ := new(big.Int).SetString(amount, 10)
	return new(big.Int).Mul(val, big.NewInt(1e18))
}

// CurrentBlock returns the latest block number.
func (m *Manager) CurrentBlock() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentBlock
}

// GetBlock returns a block by number.
func (m *Manager) GetBlock(num uint64) *insoTypes.L2Block {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.blocks[num]
}

// GetBlockByHash returns a block by its hash.
func (m *Manager) GetBlockByHash(hash common.Hash) *insoTypes.L2Block {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if num, ok := m.blockHashes[hash]; ok {
		return m.blocks[num]
	}
	return nil
}

// GetBlockHash returns the hash of a block by number.
func (m *Manager) GetBlockHash(num uint64) common.Hash {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if block, ok := m.blocks[num]; ok {
		return block.Header.Hash
	}
	return common.Hash{}
}

// GetLatestStateRoot returns the current state root.
func (m *Manager) GetLatestStateRoot() common.Hash {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stateRoot
}

// AddBlock persists a new L2 block and updates indices.
func (m *Manager) AddBlock(block *insoTypes.L2Block) {
	m.mu.Lock()
	defer m.mu.Unlock()

	num := block.Header.Number
	m.blocks[num] = block
	m.blockHashes[block.Header.Hash] = num
	m.currentBlock = num
	m.stateRoot = block.Header.StateRoot

	// Index transactions
	for _, tx := range block.Transactions {
		m.txIndex[tx.Hash()] = tx
		m.txBlockIndex[tx.Hash()] = num
	}
}

// GetTransaction returns a transaction by hash.
func (m *Manager) GetTransaction(hash common.Hash) *types.Transaction {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.txIndex[hash]
}

// GetReceipt returns a transaction receipt by hash.
func (m *Manager) GetReceipt(hash common.Hash) *types.Receipt {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.txReceipts[hash]
}

// GetBalance returns the balance for an address.
func (m *Manager) GetBalance(addr common.Address) *big.Int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if b, ok := m.balances[addr]; ok {
		return new(big.Int).Set(b)
	}
	return big.NewInt(0)
}

// GetNonce returns the nonce for an address.
func (m *Manager) GetNonce(addr common.Address) uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.nonces[addr]
}

// GetL1Origin returns the latest known L1 block origin.
func (m *Manager) GetL1Origin() insoTypes.L1Origin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.l1Origin
}

// SetL1Origin updates the L1 origin reference.
func (m *Manager) SetL1Origin(origin insoTypes.L1Origin) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.l1Origin = origin
}

// GetSequencerStatus returns the current sequencer status.
func (m *Manager) GetSequencerStatus() *insoTypes.SequencerStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return &insoTypes.SequencerStatus{
		IsSequencing:  m.isSequencing,
		CurrentBlock:  m.currentBlock,
		SequencerAddr: m.sequencerAddr,
		ChainID:       new(big.Int).SetUint64(m.chainID),
	}
}

// AddBatch records a batch submission.
func (m *Manager) AddBatch(batch *insoTypes.Batch) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.batches = append(m.batches, batch)
	m.latestBatch = batch
}

// GetLatestBatch returns the most recent batch submission.
func (m *Manager) GetLatestBatch() *insoTypes.Batch {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.latestBatch
}

// SimulateL1Submission generates a mock L1 tx hash for devnet.
func (m *Manager) SimulateL1Submission(startBlock, endBlock uint64) common.Hash {
	h := sha256.New()
	h.Write([]byte("l1-batch"))
	h.Write(new(big.Int).SetUint64(startBlock).Bytes())
	h.Write(new(big.Int).SetUint64(endBlock).Bytes())
	var hash common.Hash
	copy(hash[:], h.Sum(nil))
	return hash
}
