package state

import (
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	ethState "github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"

	"github.com/insoblok/inso-sequencer/internal/execution"
	"github.com/insoblok/inso-sequencer/internal/genesis"
	insoTypes "github.com/insoblok/inso-sequencer/pkg/types"
)

// Manager is the state manager for the L2 chain, backed by persistent storage.
// It wraps the EVM executor, state store (Pebble + MPT), and chain database.
type Manager struct {
	mu sync.RWMutex

	// Core components
	stateStore  *execution.StateStore
	chainDB     *execution.ChainDB
	executor    *execution.EVMExecutor
	chainConfig *params.ChainConfig

	// Current chain tip
	currentBlock uint64
	stateRoot    common.Hash

	// L1 origin tracking
	l1Origin insoTypes.L1Origin

	// Sequencer identity
	isSequencing  bool
	sequencerAddr common.Address
	chainID       uint64

	logger log.Logger
}

// NewManager creates a new state manager with persistent storage and EVM execution.
// It initializes from genesis if no existing state is found.
func NewManager(
	chainID uint64,
	sequencerAddr common.Address,
	stateStore *execution.StateStore,
	chainConfig *params.ChainConfig,
	gen *genesis.Genesis,
) (*Manager, error) {

	logger := log.New("module", "state")
	chainDB := execution.NewChainDB(stateStore.DiskDB())
	executor := execution.NewEVMExecutor(chainConfig)

	m := &Manager{
		stateStore:    stateStore,
		chainDB:       chainDB,
		executor:      executor,
		chainConfig:   chainConfig,
		sequencerAddr: sequencerAddr,
		chainID:       chainID,
		isSequencing:  true,
		logger:        logger,
	}

	// Check if we have existing state
	existingRoot, err := chainDB.ReadGenesisRoot()
	if err == nil && existingRoot != (common.Hash{}) {
		// Restore from existing database
		m.currentBlock = chainDB.CurrentBlock()
		m.stateRoot = chainDB.LatestStateRoot()
		logger.Info("State restored from database",
			"currentBlock", m.currentBlock,
			"stateRoot", m.stateRoot.Hex(),
		)

		// Restore L1 origin
		if origin, err := chainDB.ReadL1Origin(); err == nil && origin != nil {
			m.l1Origin = *origin
		}

		return m, nil
	}

	// No existing state -- initialize from genesis
	logger.Info("No existing state found, initializing from genesis")

	sdb, err := stateStore.OpenState(common.Hash{})
	if err != nil {
		return nil, err
	}

	genesisRoot, err := gen.InitializeState(sdb)
	if err != nil {
		return nil, err
	}

	// Commit genesis state to disk
	committedRoot, err := stateStore.CommitState(sdb, 0)
	if err != nil {
		return nil, err
	}

	if genesisRoot != committedRoot {
		logger.Warn("Genesis intermediate root differs from committed root",
			"intermediate", genesisRoot.Hex(),
			"committed", committedRoot.Hex(),
		)
	}

	m.stateRoot = committedRoot
	if err := chainDB.WriteGenesisRoot(committedRoot); err != nil {
		return nil, err
	}

	logger.Info("Genesis state initialized",
		"stateRoot", committedRoot.Hex(),
		"accounts", len(gen.Alloc),
	)

	return m, nil
}

// --- State accessors (read current state) ---

// OpenCurrentState returns a StateDB at the current state root.
func (m *Manager) OpenCurrentState() (*ethState.StateDB, error) {
	m.mu.RLock()
	root := m.stateRoot
	m.mu.RUnlock()
	return m.stateStore.OpenState(root)
}

// CurrentBlock returns the latest block number.
func (m *Manager) CurrentBlock() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentBlock
}

// GetLatestStateRoot returns the current state root.
func (m *Manager) GetLatestStateRoot() common.Hash {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stateRoot
}

// GetBlock returns a block by number.
func (m *Manager) GetBlock(num uint64) *insoTypes.L2Block {
	block, _ := m.chainDB.ReadBlock(num)
	return block
}

// GetBlockByHash returns a block by its hash.
func (m *Manager) GetBlockByHash(hash common.Hash) *insoTypes.L2Block {
	block, _ := m.chainDB.ReadBlockByHash(hash)
	return block
}

// GetBlockHash returns the hash of a block by number.
func (m *Manager) GetBlockHash(num uint64) common.Hash {
	return m.chainDB.ReadBlockHash(num)
}

// GetTransaction returns a transaction by hash.
func (m *Manager) GetTransaction(hash common.Hash) *types.Transaction {
	tx, _ := m.chainDB.ReadTransaction(hash)
	return tx
}

// GetReceipt returns a transaction receipt by hash.
func (m *Manager) GetReceipt(hash common.Hash) *types.Receipt {
	receipt, _ := m.chainDB.ReadReceipt(hash)
	return receipt
}

// GetBalance returns the balance for an address from current state.
func (m *Manager) GetBalance(addr common.Address) *big.Int {
	sdb, err := m.OpenCurrentState()
	if err != nil {
		return big.NewInt(0)
	}
	return sdb.GetBalance(addr).ToBig()
}

// GetNonce returns the nonce for an address from current state.
func (m *Manager) GetNonce(addr common.Address) uint64 {
	sdb, err := m.OpenCurrentState()
	if err != nil {
		return 0
	}
	return sdb.GetNonce(addr)
}

// GetCode returns the code for an address from current state.
func (m *Manager) GetCode(addr common.Address) []byte {
	sdb, err := m.OpenCurrentState()
	if err != nil {
		return nil
	}
	return sdb.GetCode(addr)
}

// GetStorageAt returns a storage slot value for an address.
func (m *Manager) GetStorageAt(addr common.Address, key common.Hash) common.Hash {
	sdb, err := m.OpenCurrentState()
	if err != nil {
		return common.Hash{}
	}
	return sdb.GetState(addr, key)
}

// --- EVM execution ---

// Executor returns the EVM executor for direct use (e.g., eth_call).
func (m *Manager) Executor() *execution.EVMExecutor {
	return m.executor
}

// ChainConfig returns the chain configuration.
func (m *Manager) ChainConfig() *params.ChainConfig {
	return m.chainConfig
}

// StateStore returns the underlying state store.
func (m *Manager) StateStore() *execution.StateStore {
	return m.stateStore
}

// ExecuteAndCommitBlock executes transactions, commits the state, and persists the block.
// This is the main entry point called by the block producer.
func (m *Manager) ExecuteAndCommitBlock(
	txs []*types.Transaction,
	blockNum uint64,
	timestamp uint64,
	parentHash common.Hash,
	baseFee *big.Int,
) (*insoTypes.L2Block, []*types.Transaction, error) {

	m.mu.Lock()
	defer m.mu.Unlock()

	// Open state at current root
	sdb, err := m.stateStore.OpenState(m.stateRoot)
	if err != nil {
		return nil, nil, err
	}

	signer := types.LatestSignerForChainID(new(big.Int).SetUint64(m.chainID))

	execCtx := &execution.ExecutionContext{
		BlockNumber: new(big.Int).SetUint64(blockNum),
		Timestamp:   timestamp,
		Coinbase:    m.sequencerAddr,
		GasLimit:    30_000_000, // from config
		BaseFee:     baseFee,
		ParentHash:  parentHash,
	}

	// Execute all transactions through the EVM
	results, skipped, cumulativeGas := m.executor.ExecuteTransactions(sdb, txs, execCtx, signer)

	// Commit state to get the new Merkle Patricia Trie root
	newRoot, err := m.stateStore.CommitState(sdb, blockNum)
	if err != nil {
		return nil, skipped, err
	}

	// Compute block hash
	blockHash := computeBlockHash(blockNum, newRoot, timestamp, parentHash)

	// Build receipts slice
	receipts := make([]*types.Receipt, 0, len(results))
	executedTxs := make([]*types.Transaction, 0, len(results))
	for _, r := range results {
		r.Receipt.BlockHash = blockHash
		r.Receipt.BlockNumber = new(big.Int).SetUint64(blockNum)
		receipts = append(receipts, r.Receipt)
		// Find the corresponding tx
		for _, tx := range txs {
			if tx.Hash() == r.Receipt.TxHash {
				executedTxs = append(executedTxs, tx)
				break
			}
		}
	}

	header := &insoTypes.L2BlockHeader{
		Number:        blockNum,
		Hash:          blockHash,
		ParentHash:    parentHash,
		Timestamp:     timestamp,
		StateRoot:     newRoot,
		GasUsed:       cumulativeGas,
		GasLimit:      execCtx.GasLimit,
		TxCount:       len(executedTxs),
		SequencerAddr: m.sequencerAddr,
		L1Origin:      m.l1Origin,
		BaseFee:       baseFee,
	}

	block := &insoTypes.L2Block{
		Header:       header,
		Transactions: executedTxs,
		Receipts:     receipts,
	}

	// Persist block + indices to chain database
	if err := m.chainDB.WriteBlock(block); err != nil {
		return nil, skipped, err
	}

	// Update in-memory state
	m.currentBlock = blockNum
	m.stateRoot = newRoot

	return block, skipped, nil
}

// CallContract executes a read-only call (eth_call).
func (m *Manager) CallContract(msg *core.Message, blockNum uint64) ([]byte, uint64, error) {
	m.mu.RLock()
	root := m.stateRoot
	m.mu.RUnlock()

	sdb, err := m.stateStore.OpenState(root)
	if err != nil {
		return nil, 0, err
	}

	blockCtx := m.buildBlockContext(blockNum)
	return m.executor.CallContract(sdb, msg, &blockCtx)
}

// EstimateGas estimates the gas required for a transaction.
func (m *Manager) EstimateGas(msg *core.Message, blockNum uint64) (uint64, error) {
	m.mu.RLock()
	root := m.stateRoot
	m.mu.RUnlock()

	sdb, err := m.stateStore.OpenState(root)
	if err != nil {
		return 0, err
	}

	blockCtx := m.buildBlockContext(blockNum)
	return m.executor.EstimateGas(sdb, msg, &blockCtx)
}

func (m *Manager) buildBlockContext(blockNum uint64) vm.BlockContext {
	return vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		GetHash: func(n uint64) common.Hash {
			return m.GetBlockHash(n)
		},
		Coinbase:    m.sequencerAddr,
		BlockNumber: new(big.Int).SetUint64(blockNum),
		Time:        uint64(0), // caller fills in
		GasLimit:    30_000_000,
		BaseFee:     big.NewInt(1_000_000_000),
	}
}

// --- L1 origin ---

func (m *Manager) GetL1Origin() insoTypes.L1Origin {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.l1Origin
}

func (m *Manager) SetL1Origin(origin insoTypes.L1Origin) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.l1Origin = origin
	_ = m.chainDB.WriteL1Origin(&origin)
}

// --- Sequencer status ---

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

// --- Batch operations ---

func (m *Manager) AddBatch(batch *insoTypes.Batch) {
	_ = m.chainDB.WriteBatch(batch)
}

func (m *Manager) GetLatestBatch() *insoTypes.Batch {
	batch, _ := m.chainDB.ReadLatestBatch()
	return batch
}

func (m *Manager) SimulateL1Submission(startBlock, endBlock uint64) common.Hash {
	// Compute deterministic hash for simulated submission
	h := common.BytesToHash(
		new(big.Int).Add(
			new(big.Int).SetUint64(startBlock),
			new(big.Int).SetUint64(endBlock),
		).Bytes(),
	)
	return h
}

// Close shuts down the state manager and persists all data.
func (m *Manager) Close() error {
	return m.stateStore.Close()
}

// computeBlockHash produces a deterministic block hash from block data.
func computeBlockHash(blockNum uint64, stateRoot common.Hash, timestamp uint64, parentHash common.Hash) common.Hash {
	data := make([]byte, 0, 8+32+8+32)
	data = append(data, new(big.Int).SetUint64(blockNum).Bytes()...)
	data = append(data, stateRoot.Bytes()...)
	data = append(data, new(big.Int).SetUint64(timestamp).Bytes()...)
	data = append(data, parentHash.Bytes()...)
	return common.BytesToHash(data)
}
