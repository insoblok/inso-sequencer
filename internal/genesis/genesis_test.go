package genesis

import (
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/insoblok/inso-sequencer/internal/execution"
)

func TestDefaultGenesis(t *testing.T) {
	gen := DefaultGenesis()

	if gen == nil {
		t.Fatal("expected non-nil genesis")
	}

	if len(gen.Alloc) != 4 {
		t.Errorf("expected 4 alloc accounts, got %d", len(gen.Alloc))
	}

	chainCfg := gen.ChainConfig()
	if chainCfg.ChainID.Cmp(big.NewInt(42069)) != 0 {
		t.Errorf("expected chain ID 42069, got %s", chainCfg.ChainID)
	}
}

func TestDefaultChainConfig(t *testing.T) {
	cfg := DefaultChainConfig()

	if cfg.ChainID.Cmp(big.NewInt(42069)) != 0 {
		t.Errorf("expected chain ID 42069, got %s", cfg.ChainID)
	}

	// All forks should be enabled at block 0
	if cfg.HomesteadBlock.Cmp(big.NewInt(0)) != 0 {
		t.Error("homestead should be at block 0")
	}
	if cfg.ByzantiumBlock.Cmp(big.NewInt(0)) != 0 {
		t.Error("byzantium should be at block 0")
	}
	if cfg.LondonBlock.Cmp(big.NewInt(0)) != 0 {
		t.Error("london should be at block 0")
	}
}

func TestLoadGenesis_FileNotFound(t *testing.T) {
	_, err := LoadGenesis("/nonexistent/genesis.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadGenesis_ValidFile(t *testing.T) {
	// Write a minimal genesis file
	tmpDir := t.TempDir()
	genesisPath := filepath.Join(tmpDir, "genesis.json")
	content := `{
		"config": {"chainId": 42069},
		"alloc": {
			"0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266": {
				"balance": "0x21E19E0C9BAB2400000"
			}
		},
		"gasLimit": "0x1c9c380"
	}`
	if err := os.WriteFile(genesisPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write genesis: %v", err)
	}

	gen, err := LoadGenesis(genesisPath)
	if err != nil {
		t.Fatalf("failed to load genesis: %v", err)
	}

	if len(gen.Alloc) != 1 {
		t.Errorf("expected 1 alloc account, got %d", len(gen.Alloc))
	}
}

func TestParseGasLimit(t *testing.T) {
	gen := DefaultGenesis()
	gen.GasLimit = "0x1c9c380" // 30000000

	gasLimit := gen.ParseGasLimit()
	if gasLimit != 30000000 {
		t.Errorf("expected 30000000, got %d", gasLimit)
	}
}

func TestParseGasLimit_Default(t *testing.T) {
	gen := &Genesis{}
	gasLimit := gen.ParseGasLimit()
	if gasLimit != 30_000_000 {
		t.Errorf("expected default 30000000, got %d", gasLimit)
	}
}

func TestInitializeState(t *testing.T) {
	gen := DefaultGenesis()

	store, err := execution.NewStateStore("")
	if err != nil {
		t.Fatalf("failed to create state store: %v", err)
	}
	defer store.Close()

	sdb, err := store.OpenState(common.Hash{})
	if err != nil {
		t.Fatalf("failed to open state: %v", err)
	}

	root, err := gen.InitializeState(sdb)
	if err != nil {
		t.Fatalf("InitializeState failed: %v", err)
	}

	if root == (common.Hash{}) {
		t.Error("state root should not be empty after initialization")
	}

	// Check deployer account has balance
	deployer := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	balance := sdb.GetBalance(deployer).ToBig()
	if balance.Sign() <= 0 {
		t.Error("deployer should have positive balance")
	}

	// Check validator has more balance
	validator := common.HexToAddress("0x90F79bf6EB2c4f870365E785982E1f101E93b906")
	vBalance := sdb.GetBalance(validator).ToBig()
	if vBalance.Sign() <= 0 {
		t.Error("validator should have positive balance")
	}

	// Validator should have more than deployer
	if vBalance.Cmp(balance) <= 0 {
		t.Error("validator should have more balance than deployer")
	}
}
