package genesis

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// Genesis represents the genesis block configuration.
type Genesis struct {
	Config     *params.ChainConfig          `json:"config"`
	Nonce      string                       `json:"nonce"`
	Timestamp  string                       `json:"timestamp"`
	ExtraData  string                       `json:"extraData"`
	GasLimit   string                       `json:"gasLimit"`
	Difficulty string                       `json:"difficulty"`
	MixHash    string                       `json:"mixHash"`
	Coinbase   string                       `json:"coinbase"`
	Alloc      map[string]GenesisAccount    `json:"alloc"`
}

// GenesisAccount represents a pre-funded account in genesis.
type GenesisAccount struct {
	Balance string            `json:"balance"`
	Code    string            `json:"code,omitempty"`
	Nonce   uint64            `json:"nonce,omitempty"`
	Storage map[string]string `json:"storage,omitempty"`
}

// LoadGenesis reads and parses a genesis.json file.
func LoadGenesis(path string) (*Genesis, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read genesis file: %w", err)
	}

	var gen Genesis
	if err := json.Unmarshal(data, &gen); err != nil {
		return nil, fmt.Errorf("parse genesis: %w", err)
	}

	return &gen, nil
}

// DefaultGenesis returns the default INSO L2 genesis for devnet.
func DefaultGenesis() *Genesis {
	return &Genesis{
		Config: DefaultChainConfig(),
		Alloc: map[string]GenesisAccount{
			"0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266": {Balance: "0x21E19E0C9BAB2400000"},
			"0x70997970C51812dc3A010C7d01b50e0d17dc79C8": {Balance: "0x21E19E0C9BAB2400000"},
			"0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC": {Balance: "0x21E19E0C9BAB2400000"},
			"0x90F79bf6EB2c4f870365E785982E1f101E93b906": {Balance: "0x152D02C7E14AF6800000"},
		},
		GasLimit:   "0x1c9c380",
		Difficulty: "0x0",
	}
}

// DefaultChainConfig returns the INSO L2 chain config matching genesis.json.
func DefaultChainConfig() *params.ChainConfig {
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
		ShanghaiTime:        newUint64(0),
		CancunTime:          newUint64(0),
	}
}

func newUint64(v uint64) *uint64 {
	return &v
}

// ChainConfig returns the parsed chain config from the genesis, or defaults.
func (g *Genesis) ChainConfig() *params.ChainConfig {
	if g.Config != nil {
		return g.Config
	}
	return DefaultChainConfig()
}

// ParseGasLimit returns the gas limit as uint64.
func (g *Genesis) ParseGasLimit() uint64 {
	val, _ := new(big.Int).SetString(g.GasLimit, 0)
	if val == nil {
		return 30_000_000 // default
	}
	return val.Uint64()
}

// InitializeState applies the genesis allocations to a fresh StateDB.
// Returns the state root after all allocations are applied.
func (g *Genesis) InitializeState(sdb *state.StateDB) (common.Hash, error) {
	logger := log.New("module", "genesis")
	logger.Info("Initializing genesis state", "accounts", len(g.Alloc))

	for addrHex, account := range g.Alloc {
		addr := common.HexToAddress(addrHex)

		// Set balance
		balance, ok := new(big.Int).SetString(account.Balance, 0)
		if !ok {
			return common.Hash{}, fmt.Errorf("invalid balance for %s: %s", addrHex, account.Balance)
		}
		balanceU256, _ := uint256.FromBig(balance)
		sdb.AddBalance(addr, balanceU256, tracing.BalanceIncreaseGenesisBalance)

		// Set nonce
		if account.Nonce > 0 {
			sdb.SetNonce(addr, account.Nonce)
		}

		// Set code
		if account.Code != "" {
			code := common.FromHex(account.Code)
			sdb.SetCode(addr, code)
		}

		// Set storage
		for keyHex, valHex := range account.Storage {
			key := common.HexToHash(keyHex)
			val := common.HexToHash(valHex)
			sdb.SetState(addr, key, val)
		}

		logger.Debug("Genesis account initialized",
			"address", addr.Hex(),
			"balance", balance.String(),
		)
	}

	return sdb.IntermediateRoot(true), nil
}
