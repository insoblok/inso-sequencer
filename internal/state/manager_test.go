package state

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/holiman/uint256"

	"github.com/insoblok/inso-sequencer/internal/execution"
	"github.com/insoblok/inso-sequencer/internal/genesis"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()

	store, err := execution.NewStateStore("")
	if err != nil {
		t.Fatalf("create state store: %v", err)
	}

	gen := genesis.DefaultGenesis()
	chainConfig := gen.ChainConfig()

	sequencerAddr := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")

	m, err := NewManager(42069, sequencerAddr, store, chainConfig, gen)
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}

	return m
}

func TestNewManager_InitializesGenesis(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()

	// Block number should be 0 (genesis)
	if m.CurrentBlock() != 0 {
		t.Errorf("expected block 0, got %d", m.CurrentBlock())
	}

	// State root should not be empty
	root := m.GetLatestStateRoot()
	if root == (common.Hash{}) {
		t.Error("state root should not be empty after genesis")
	}

	// Genesis accounts should have balances
	deployer := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	balance := m.GetBalance(deployer)
	if balance.Sign() <= 0 {
		t.Error("deployer should have positive balance")
	}

	// Nonce should be 0
	nonce := m.GetNonce(deployer)
	if nonce != 0 {
		t.Errorf("expected nonce 0, got %d", nonce)
	}
}

func TestManager_ExecuteAndCommitBlock_Empty(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()

	parentHash := m.GetBlockHash(0) // will be empty for block 0 since we don't store genesis as a block
	baseFee := big.NewInt(1_000_000_000)

	block, skipped, err := m.ExecuteAndCommitBlock(nil, 1, 1000, parentHash, baseFee)
	if err != nil {
		t.Fatalf("execute empty block: %v", err)
	}

	if block == nil {
		t.Fatal("block should not be nil")
	}
	if block.Header.Number != 1 {
		t.Errorf("expected block 1, got %d", block.Header.Number)
	}
	if block.Header.TxCount != 0 {
		t.Errorf("expected 0 txs, got %d", block.Header.TxCount)
	}
	if len(skipped) != 0 {
		t.Errorf("expected 0 skipped, got %d", len(skipped))
	}

	// Current block should advance
	if m.CurrentBlock() != 1 {
		t.Errorf("expected current block 1, got %d", m.CurrentBlock())
	}
}

func TestManager_ExecuteAndCommitBlock_Transfer(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()

	// Use one of the genesis-funded accounts (deployer has 10000 ETH)
	// We'll generate a fresh key and fund it via a block that writes to state
	senderKey, _ := crypto.GenerateKey()
	sender := crypto.PubkeyToAddress(senderKey.PublicKey)

	// Fund sender by directly modifying state (simulating a deposit)
	// We need to commit this change so it's reflected in the manager's state root
	func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		sdb, err := m.stateStore.OpenState(m.stateRoot)
		if err != nil {
			t.Fatalf("open state: %v", err)
		}
		fundAmt, _ := uint256.FromBig(new(big.Int).Mul(big.NewInt(100), big.NewInt(1e18)))
		sdb.AddBalance(sender, fundAmt, tracing.BalanceIncreaseGenesisBalance)
		newRoot, err := m.stateStore.CommitState(sdb, 0)
		if err != nil {
			t.Fatalf("commit seed state: %v", err)
		}
		m.stateRoot = newRoot
	}()

	// Create a transfer transaction
	recipient := common.HexToAddress("0xdeadbeef00000000000000000000000000000001")
	chainID := big.NewInt(42069)
	signer := types.LatestSignerForChainID(chainID)

	tx, err := types.SignTx(
		types.NewTransaction(0, recipient, big.NewInt(1e18), 21000, big.NewInt(1_000_000_000), nil),
		signer,
		senderKey,
	)
	if err != nil {
		t.Fatalf("sign tx: %v", err)
	}

	parentHash := common.Hash{}
	baseFee := big.NewInt(1_000_000_000)

	block, skipped, err := m.ExecuteAndCommitBlock(
		[]*types.Transaction{tx}, 1, 1000, parentHash, baseFee,
	)
	if err != nil {
		t.Fatalf("execute block: %v", err)
	}

	if block.Header.TxCount != 1 {
		t.Errorf("expected 1 tx, got %d", block.Header.TxCount)
	}
	if len(skipped) != 0 {
		t.Errorf("expected 0 skipped, got %d", len(skipped))
	}
	if block.Header.GasUsed != 21000 {
		t.Errorf("expected 21000 gas, got %d", block.Header.GasUsed)
	}

	// Verify receipt
	if len(block.Receipts) != 1 {
		t.Fatalf("expected 1 receipt, got %d", len(block.Receipts))
	}
	if block.Receipts[0].Status != types.ReceiptStatusSuccessful {
		t.Error("expected successful receipt")
	}

	// Verify recipient balance
	recipientBal := m.GetBalance(recipient)
	if recipientBal.Cmp(big.NewInt(1e18)) != 0 {
		t.Errorf("expected recipient balance 1e18, got %s", recipientBal.String())
	}
}

func TestManager_GetCode_NoCode(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()

	addr := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	code := m.GetCode(addr)
	if len(code) != 0 {
		t.Errorf("expected empty code for EOA, got %d bytes", len(code))
	}
}

func TestManager_GetStorageAt_Empty(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()

	addr := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	key := common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001")
	val := m.GetStorageAt(addr, key)
	if val != (common.Hash{}) {
		t.Errorf("expected empty storage, got %s", val.Hex())
	}
}

func TestManager_SequencerStatus(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()

	status := m.GetSequencerStatus()
	if status == nil {
		t.Fatal("expected non-nil status")
	}
	if !status.IsSequencing {
		t.Error("expected sequencing to be true")
	}
	if status.ChainID.Cmp(big.NewInt(42069)) != 0 {
		t.Errorf("expected chain ID 42069, got %s", status.ChainID)
	}
}

func TestManager_L1Origin(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()

	origin := m.GetL1Origin()
	if origin.BlockNumber != 0 {
		t.Errorf("expected L1 origin block 0, got %d", origin.BlockNumber)
	}
}

func TestManager_ChainDB_Persistence(t *testing.T) {
	m := newTestManager(t)
	defer m.Close()

	baseFee := big.NewInt(1_000_000_000)

	// Produce a few empty blocks
	for i := uint64(1); i <= 3; i++ {
		_, _, err := m.ExecuteAndCommitBlock(nil, i, 1000+i, common.Hash{}, baseFee)
		if err != nil {
			t.Fatalf("execute block %d: %v", i, err)
		}
	}

	// Read back blocks from ChainDB
	block1 := m.GetBlock(1)
	if block1 == nil {
		t.Fatal("block 1 should exist")
	}
	if block1.Header.Number != 1 {
		t.Errorf("expected block 1, got %d", block1.Header.Number)
	}

	block3 := m.GetBlock(3)
	if block3 == nil {
		t.Fatal("block 3 should exist")
	}

	// Get block by hash
	hash := block3.Header.Hash
	byHash := m.GetBlockByHash(hash)
	if byHash == nil {
		t.Fatalf("expected block by hash %s", hash.Hex())
	}
	if byHash.Header.Number != 3 {
		t.Errorf("expected block 3, got %d", byHash.Header.Number)
	}
}
