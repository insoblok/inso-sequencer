package execution

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"math/big"

	"github.com/holiman/uint256"
)

func TestNewStateStore_InMemory(t *testing.T) {
	store, err := NewStateStore("")
	if err != nil {
		t.Fatalf("failed to create in-memory state store: %v", err)
	}
	defer store.Close()

	if store.disk == nil {
		t.Fatal("disk database should not be nil")
	}
	if store.trieDB == nil {
		t.Fatal("trie database should not be nil")
	}
}

func TestStateStore_OpenState_EmptyRoot(t *testing.T) {
	store, err := NewStateStore("")
	if err != nil {
		t.Fatalf("failed to create state store: %v", err)
	}
	defer store.Close()

	sdb, err := store.OpenState(common.Hash{})
	if err != nil {
		t.Fatalf("failed to open state at empty root: %v", err)
	}
	if sdb == nil {
		t.Fatal("StateDB should not be nil")
	}
}

func TestStateStore_CommitAndReopen(t *testing.T) {
	store, err := NewStateStore("")
	if err != nil {
		t.Fatalf("failed to create state store: %v", err)
	}
	defer store.Close()

	// Open fresh state and add an account balance
	sdb, err := store.OpenState(common.Hash{})
	if err != nil {
		t.Fatalf("failed to open state: %v", err)
	}

	addr := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	expectedBalance := new(big.Int).Mul(big.NewInt(10000), big.NewInt(1e18))
	balU256, _ := uint256.FromBig(expectedBalance)
	sdb.AddBalance(addr, balU256, tracing.BalanceIncreaseGenesisBalance)

	// Commit to get state root
	root, err := store.CommitState(sdb, 0)
	if err != nil {
		t.Fatalf("failed to commit state: %v", err)
	}

	if root == (common.Hash{}) {
		t.Fatal("committed root should not be empty")
	}

	// Reopen state at committed root
	sdb2, err := store.OpenState(root)
	if err != nil {
		t.Fatalf("failed to reopen state at root %s: %v", root.Hex(), err)
	}

	// Verify balance persisted
	balance := sdb2.GetBalance(addr).ToBig()
	if balance.Cmp(expectedBalance) != 0 {
		t.Errorf("balance mismatch after reopen: expected %s, got %s", expectedBalance.String(), balance.String())
	}
}

func TestStateStore_HasState(t *testing.T) {
	store, err := NewStateStore("")
	if err != nil {
		t.Fatalf("failed to create state store: %v", err)
	}
	defer store.Close()

	// Commit some state and verify we can reopen it (functional test)
	sdb, _ := store.OpenState(common.Hash{})
	addr := common.HexToAddress("0xdeadbeef")
	bal, _ := uint256.FromBig(big.NewInt(1e18))
	sdb.AddBalance(addr, bal, tracing.BalanceIncreaseGenesisBalance)
	root, _ := store.CommitState(sdb, 0)

	// We should be able to reopen at this root
	sdb2, err := store.OpenState(root)
	if err != nil {
		t.Fatalf("should be able to reopen committed root: %v", err)
	}
	gotBal := sdb2.GetBalance(addr).ToBig()
	if gotBal.Cmp(big.NewInt(1e18)) != 0 {
		t.Errorf("balance mismatch: expected 1e18, got %s", gotBal.String())
	}

	// Random root should fail to open
	randomRoot := common.HexToHash("0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	_, err = store.OpenState(randomRoot)
	if err == nil {
		t.Error("opening random root should fail")
	}
}

func TestStateStore_DiskDB(t *testing.T) {
	store, err := NewStateStore("")
	if err != nil {
		t.Fatalf("failed to create state store: %v", err)
	}
	defer store.Close()

	if store.DiskDB() == nil {
		t.Fatal("DiskDB should not return nil")
	}
}
