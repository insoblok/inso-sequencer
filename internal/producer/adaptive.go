package producer

import (
	"math"
	"sync"

	"github.com/ethereum/go-ethereum/log"
)

// AdaptiveBlockSizer dynamically adjusts block gas limits and timing
// based on network demand. When the mempool is congested, blocks expand
// (up to a ceiling). When demand is low, blocks shrink to save resources.
//
// Feature #4 from InSoBlok L2 Future Work
type AdaptiveBlockSizer struct {
	mu sync.RWMutex

	// Current dynamic values
	currentGasLimit uint64
	currentMaxTx    int

	// Configuration bounds
	minGasLimit uint64
	maxGasLimit uint64
	minMaxTx    int
	maxMaxTx    int

	// EMA of gas utilization (0-1)
	utilizationEMA float64

	// Smoothing factor for EMA (0-1, higher = more reactive)
	alpha float64

	// Target utilization (expand above, shrink below)
	targetUtilization float64

	// Step size for adjustments (basis points of range)
	adjustStepBps uint64

	logger log.Logger
}

// AdaptiveSizerConfig holds configuration for AdaptiveBlockSizer.
type AdaptiveSizerConfig struct {
	MinGasLimit       uint64  // Minimum gas limit (e.g., 15M)
	MaxGasLimit       uint64  // Maximum gas limit (e.g., 60M)
	InitialGasLimit   uint64  // Starting gas limit (e.g., 30M)
	MinMaxTx          int     // Minimum max transactions per block
	MaxMaxTx          int     // Maximum max transactions per block
	InitialMaxTx      int     // Starting max txs per block
	Alpha             float64 // EMA smoothing (0.1 = slow, 0.5 = fast)
	TargetUtilization float64 // Target gas utilization (e.g., 0.5)
	AdjustStepBps     uint64  // Step size in basis points (e.g., 500 = 5%)
}

// DefaultAdaptiveSizerConfig returns sensible defaults.
func DefaultAdaptiveSizerConfig() AdaptiveSizerConfig {
	return AdaptiveSizerConfig{
		MinGasLimit:       15_000_000,  // 15M
		MaxGasLimit:       60_000_000,  // 60M
		InitialGasLimit:   30_000_000,  // 30M
		MinMaxTx:          1_000,
		MaxMaxTx:          10_000,
		InitialMaxTx:      5_000,
		Alpha:             0.2,
		TargetUtilization: 0.5,
		AdjustStepBps:     500, // 5% step
	}
}

// NewAdaptiveBlockSizer creates a new adaptive block sizing engine.
func NewAdaptiveBlockSizer(cfg AdaptiveSizerConfig) *AdaptiveBlockSizer {
	return &AdaptiveBlockSizer{
		currentGasLimit:   cfg.InitialGasLimit,
		currentMaxTx:      cfg.InitialMaxTx,
		minGasLimit:       cfg.MinGasLimit,
		maxGasLimit:       cfg.MaxGasLimit,
		minMaxTx:          cfg.MinMaxTx,
		maxMaxTx:          cfg.MaxMaxTx,
		utilizationEMA:    cfg.TargetUtilization,
		alpha:             cfg.Alpha,
		targetUtilization: cfg.TargetUtilization,
		adjustStepBps:     cfg.AdjustStepBps,
		logger:            log.New("module", "adaptive-block"),
	}
}

// AdjustAfterBlock updates the block size based on the actual gas used
// in the last produced block. Call this after every block production.
func (a *AdaptiveBlockSizer) AdjustAfterBlock(gasUsed, gasLimit uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if gasLimit == 0 {
		return
	}

	// Compute utilization for this block
	utilization := float64(gasUsed) / float64(gasLimit)

	// Update EMA
	a.utilizationEMA = a.alpha*utilization + (1-a.alpha)*a.utilizationEMA

	// Determine adjustment direction
	gasRange := a.maxGasLimit - a.minGasLimit
	step := gasRange * a.adjustStepBps / 10000

	txRange := a.maxMaxTx - a.minMaxTx
	txStep := int(float64(txRange) * float64(a.adjustStepBps) / 10000.0)
	if txStep < 1 {
		txStep = 1
	}

	if a.utilizationEMA > a.targetUtilization+0.1 {
		// High demand → expand blocks
		a.currentGasLimit = min64(a.currentGasLimit+step, a.maxGasLimit)
		a.currentMaxTx = minInt(a.currentMaxTx+txStep, a.maxMaxTx)
		a.logger.Debug("Block size expanded",
			"gasLimit", a.currentGasLimit,
			"maxTx", a.currentMaxTx,
			"utilization", math.Round(a.utilizationEMA*100)/100,
		)
	} else if a.utilizationEMA < a.targetUtilization-0.1 {
		// Low demand → shrink blocks
		a.currentGasLimit = max64(a.currentGasLimit-step, a.minGasLimit)
		a.currentMaxTx = maxInt(a.currentMaxTx-txStep, a.minMaxTx)
		a.logger.Debug("Block size shrunk",
			"gasLimit", a.currentGasLimit,
			"maxTx", a.currentMaxTx,
			"utilization", math.Round(a.utilizationEMA*100)/100,
		)
	}
}

// CurrentGasLimit returns the current adaptive gas limit.
func (a *AdaptiveBlockSizer) CurrentGasLimit() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.currentGasLimit
}

// CurrentMaxTx returns the current adaptive max transactions per block.
func (a *AdaptiveBlockSizer) CurrentMaxTx() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.currentMaxTx
}

// UtilizationEMA returns the current EMA of block gas utilization.
func (a *AdaptiveBlockSizer) UtilizationEMA() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.utilizationEMA
}

// Stats returns current adaptive block sizing stats.
func (a *AdaptiveBlockSizer) Stats() (gasLimit uint64, maxTx int, utilization float64) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.currentGasLimit, a.currentMaxTx, a.utilizationEMA
}

func min64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func max64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
