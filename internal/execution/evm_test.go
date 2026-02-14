package execution

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// testChainConfig returns the INSO L2 chain config for tests.
func testChainConfig() *params.ChainConfig {
	return &params.ChainConfig{
		ChainID:             big.NewInt(42069),
		HomesteadBlock:      big.NewInt(0),
		EIP150Block:         big.NewInt(0),
		EIP155Block:         big.NewInt(0),
		EIP158Block:         big.NewInt(0),
		ByzantiumBlock:      big.NewInt(0),
		ConstantinopleBlock: big.NewInt(0),
		PetersburgBlock:     big.NewInt(0),
		IstanbulBlock:       big.NewInt(0),
		BerlinBlock:         big.NewInt(0),
		LondonBlock:         big.NewInt(0),
	}
}

func TestNewEVMExecutor(t *testing.T) {
	cfg := testChainConfig()
	exec := NewEVMExecutor(cfg)
	if exec == nil {
		t.Fatal("expected non-nil executor")
	}
	if exec.chainConfig.ChainID.Cmp(big.NewInt(42069)) != 0 {
		t.Errorf("expected chain ID 42069, got %s", exec.chainConfig.ChainID)
	}
}

func TestExecuteTransactions_EmptyBlock(t *testing.T) {
	cfg := testChainConfig()
	exec := NewEVMExecutor(cfg)

	// Create in-memory state store
	store, err := NewStateStore("")
	if err != nil {
		t.Fatalf("failed to create state store: %v", err)
	}
	defer store.Close()

	sdb, err := store.OpenState(common.Hash{})
	if err != nil {
		t.Fatalf("failed to open state: %v", err)
	}

	signer := types.LatestSignerForChainID(big.NewInt(42069))

	ctx := &ExecutionContext{
		BlockNumber: big.NewInt(1),
		Timestamp:   1000,
		Coinbase:    common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"),
		GasLimit:    30_000_000,
		BaseFee:     big.NewInt(1_000_000_000),
		ParentHash:  common.Hash{},
	}

	executed, skipped, gas := exec.ExecuteTransactions(sdb, nil, ctx, signer)

	if len(executed) != 0 {
		t.Errorf("expected 0 executed txs, got %d", len(executed))
	}
	if len(skipped) != 0 {
		t.Errorf("expected 0 skipped txs, got %d", len(skipped))
	}
	if gas != 0 {
		t.Errorf("expected 0 gas, got %d", gas)
	}
}

func TestExecuteTransactions_SimpleTransfer(t *testing.T) {
	cfg := testChainConfig()
	exec := NewEVMExecutor(cfg)

	store, err := NewStateStore("")
	if err != nil {
		t.Fatalf("failed to create state store: %v", err)
	}
	defer store.Close()

	sdb, err := store.OpenState(common.Hash{})
	if err != nil {
		t.Fatalf("failed to open state: %v", err)
	}

	// Fund sender account
	senderKey, _ := crypto.GenerateKey()
	sender := crypto.PubkeyToAddress(senderKey.PublicKey)
	initBal, _ := uint256.FromBig(new(big.Int).Mul(big.NewInt(10), big.NewInt(1e18)))
	sdb.AddBalance(sender, initBal, tracing.BalanceIncreaseGenesisBalance)
	sdb.SetNonce(sender, 0)

	recipient := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	// Create signed transfer tx
	chainID := big.NewInt(42069)
	signer := types.LatestSignerForChainID(chainID)
	tx, err := types.SignTx(
		types.NewTransaction(
			0,                          // nonce
			recipient,                  // to
			big.NewInt(1e18),           // value: 1 ETH
			21000,                      // gas
			big.NewInt(1_000_000_000),  // gas price: 1 gwei
			nil,                        // data
		),
		signer,
		senderKey,
	)
	if err != nil {
		t.Fatalf("failed to sign tx: %v", err)
	}

	ctx := &ExecutionContext{
		BlockNumber: big.NewInt(1),
		Timestamp:   1000,
		Coinbase:    common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"),
		GasLimit:    30_000_000,
		BaseFee:     big.NewInt(1_000_000_000),
		ParentHash:  common.Hash{},
	}

	executed, skipped, gas := exec.ExecuteTransactions(sdb, []*types.Transaction{tx}, ctx, signer)

	if len(skipped) != 0 {
		t.Errorf("expected 0 skipped, got %d", len(skipped))
	}
	if len(executed) != 1 {
		t.Fatalf("expected 1 executed, got %d", len(executed))
	}
	if executed[0].Receipt.Status != types.ReceiptStatusSuccessful {
		t.Errorf("expected successful receipt, got status %d", executed[0].Receipt.Status)
	}
	if gas != 21000 {
		t.Errorf("expected 21000 gas used, got %d", gas)
	}

	// Check recipient received 1 ETH
	balance := sdb.GetBalance(recipient)
	expected := big.NewInt(1e18)
	if balance.ToBig().Cmp(expected) != 0 {
		t.Errorf("recipient balance: expected %s, got %s", expected.String(), balance.String())
	}
}

func TestExecuteTransactions_InsufficientBalance(t *testing.T) {
	cfg := testChainConfig()
	exec := NewEVMExecutor(cfg)

	store, err := NewStateStore("")
	if err != nil {
		t.Fatalf("failed to create state store: %v", err)
	}
	defer store.Close()

	sdb, err := store.OpenState(common.Hash{})
	if err != nil {
		t.Fatalf("failed to open state: %v", err)
	}

	// Fund sender with only a tiny amount
	senderKey, _ := crypto.GenerateKey()
	sender := crypto.PubkeyToAddress(senderKey.PublicKey)
	tinyBalance, _ := uint256.FromBig(big.NewInt(1000))
	sdb.AddBalance(sender, tinyBalance, tracing.BalanceIncreaseGenesisBalance)
	sdb.SetNonce(sender, 0)

	recipient := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	chainID := big.NewInt(42069)
	signer := types.LatestSignerForChainID(chainID)
	tx, _ := types.SignTx(
		types.NewTransaction(0, recipient, big.NewInt(1e18), 21000, big.NewInt(1_000_000_000), nil),
		signer,
		senderKey,
	)

	ctx := &ExecutionContext{
		BlockNumber: big.NewInt(1),
		Timestamp:   1000,
		Coinbase:    common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"),
		GasLimit:    30_000_000,
		BaseFee:     big.NewInt(1_000_000_000),
		ParentHash:  common.Hash{},
	}

	executed, skipped, _ := exec.ExecuteTransactions(sdb, []*types.Transaction{tx}, ctx, signer)

	if len(executed) != 0 {
		t.Errorf("expected 0 executed (insufficient balance), got %d", len(executed))
	}
	if len(skipped) != 1 {
		t.Errorf("expected 1 skipped, got %d", len(skipped))
	}
}

func TestExecuteTransactions_WrongNonce(t *testing.T) {
	cfg := testChainConfig()
	exec := NewEVMExecutor(cfg)

	store, err := NewStateStore("")
	if err != nil {
		t.Fatalf("failed to create state store: %v", err)
	}
	defer store.Close()

	sdb, err := store.OpenState(common.Hash{})
	if err != nil {
		t.Fatalf("failed to open state: %v", err)
	}

	senderKey, _ := crypto.GenerateKey()
	sender := crypto.PubkeyToAddress(senderKey.PublicKey)
	nonceTestBal, _ := uint256.FromBig(new(big.Int).Mul(big.NewInt(10), big.NewInt(1e18)))
	sdb.AddBalance(sender, nonceTestBal, tracing.BalanceIncreaseGenesisBalance)
	sdb.SetNonce(sender, 0)

	recipient := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	chainID := big.NewInt(42069)
	signer := types.LatestSignerForChainID(chainID)

	// Create tx with wrong nonce (5 instead of 0)
	tx, _ := types.SignTx(
		types.NewTransaction(5, recipient, big.NewInt(1e18), 21000, big.NewInt(1_000_000_000), nil),
		signer,
		senderKey,
	)

	ctx := &ExecutionContext{
		BlockNumber: big.NewInt(1),
		Timestamp:   1000,
		Coinbase:    common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"),
		GasLimit:    30_000_000,
		BaseFee:     big.NewInt(1_000_000_000),
		ParentHash:  common.Hash{},
	}

	executed, skipped, _ := exec.ExecuteTransactions(sdb, []*types.Transaction{tx}, ctx, signer)

	if len(executed) != 0 {
		t.Errorf("expected 0 executed (nonce mismatch), got %d", len(executed))
	}
	if len(skipped) != 1 {
		t.Errorf("expected 1 skipped, got %d", len(skipped))
	}
}
