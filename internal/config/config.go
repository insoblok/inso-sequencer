package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the top-level sequencer configuration.
type Config struct {
	Sequencer   SequencerConfig   `yaml:"sequencer"`
	L1          L1Config          `yaml:"l1"`
	TasteScore  TasteScoreConfig  `yaml:"tastescore"`
	DID         DIDConfig         `yaml:"did"`
	Sovereignty SovereigntyConfig `yaml:"sovereignty"`
	Logging     LoggingConfig     `yaml:"logging"`
	Metrics     MetricsConfig     `yaml:"metrics"`
}

// SequencerConfig holds the core sequencer settings.
type SequencerConfig struct {
	ListenAddr    string        `yaml:"listen_addr"`
	WSAddr        string        `yaml:"ws_addr"`
	BlockTime     time.Duration `yaml:"block_time"`
	MaxBlockGas   uint64        `yaml:"max_block_gas"`
	MaxTxPerBlock int           `yaml:"max_tx_per_block"`
	ChainID       uint64        `yaml:"chain_id"`
	DataDir       string        `yaml:"datadir"`
}

// L1Config holds the L1 (Ethereum) connection settings.
type L1Config struct {
	RPCURL             string        `yaml:"rpc_url"`
	ChainID            uint64        `yaml:"chain_id"`
	BatchInboxAddr     string        `yaml:"batch_inbox_addr"`
	BatchSubmitterKey  string        `yaml:"batch_submitter_key"`
	SubmissionInterval time.Duration `yaml:"submission_interval"`
}

// TasteScoreConfig holds TasteScore integration settings.
type TasteScoreConfig struct {
	Enabled        bool          `yaml:"enabled"`
	APIURL         string        `yaml:"api_url"`
	APIKey         string        `yaml:"api_key"`
	OrderingWeight float64       `yaml:"ordering_weight"`
	CacheTTL       time.Duration `yaml:"cache_ttl"`
}

// LoggingConfig holds logging settings.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// MetricsConfig holds Prometheus metrics settings.
type MetricsConfig struct {
	Enabled bool   `yaml:"enabled"`
	Addr    string `yaml:"addr"`
}

// DIDConfig holds DID (Decentralized Identity) integration settings.
type DIDConfig struct {
	Enabled         bool   `yaml:"enabled"`
	RegistryAddress string `yaml:"registry_address"` // DIDRegistry contract address
}

// SovereigntyConfig holds sovereignty engine settings.
type SovereigntyConfig struct {
	Enabled        bool   `yaml:"enabled"`
	EngineAddress  string `yaml:"engine_address"`   // SovereigntyEngine contract address
	OracleAddress  string `yaml:"oracle_address"`   // TasteScoreOracle contract address
	SBTAddress     string `yaml:"sbt_address"`      // InSoSBT contract address
	FeeDiscounts   bool   `yaml:"fee_discounts"`    // Enable sovereignty-based fee discounts
}

// Load reads and parses a YAML config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	// Expand environment variables
	expanded := os.ExpandEnv(string(data))

	cfg := DefaultConfig()
	if err := yaml.Unmarshal([]byte(expanded), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}

// DefaultConfig returns sensible defaults for local development.
func DefaultConfig() *Config {
	return &Config{
		Sequencer: SequencerConfig{
			ListenAddr:    "0.0.0.0:8545",
			WSAddr:        "0.0.0.0:8546",
			BlockTime:     500 * time.Millisecond,
			MaxBlockGas:   30_000_000,
			MaxTxPerBlock: 5000,
			ChainID:       42069,
			DataDir:       "/data",
		},
		L1: L1Config{
			RPCURL:             "http://localhost:8551",
			ChainID:            1,
			SubmissionInterval: 30 * time.Second,
		},
		TasteScore: TasteScoreConfig{
			Enabled:        true,
			APIURL:         "https://api.insoblokai.io",
			OrderingWeight: 0.3,
			CacheTTL:       60 * time.Second,
		},
		DID: DIDConfig{
			Enabled: true,
		},
		Sovereignty: SovereigntyConfig{
			Enabled:      true,
			FeeDiscounts: true,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Addr:    "0.0.0.0:6060",
		},
	}
}
