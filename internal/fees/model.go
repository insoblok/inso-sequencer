package fees

import (
	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"
)

// DynamicFeeModel computes base fees and per-sender discounts using
// TasteScore tiers, sovereignty tiers, and block gas utilization.
//
// Base fee adjusts like EIP-1559: goes up when blocks are >50% full,
// down when they're <50% full through an exponential moving average.
//
// Sovereignty tiers provide fee discounts (basis points):
//   - Bronze:   5%  off
//   - Silver:  15%  off
//   - Gold:    25%  off
//   - Platinum: 40% off
type DynamicFeeModel struct {
	mu sync.RWMutex

	// Current base fee (in wei)
	baseFee *big.Int

	// Config
	minBaseFee *big.Int // floor: 100 gwei
	maxBaseFee *big.Int // ceiling: 100 gwei
	targetGasUsage float64 // 0.5 = 50% utilization target

	// EMA of gas utilization (0.0–1.0)
	emaGasUsage float64
	emaAlpha    float64 // smoothing factor

	// Sovereignty discount cache: address → discount bps (0-10000)
	discountCache map[common.Address]discountEntry
	discountTTL   time.Duration

	logger log.Logger
}

type discountEntry struct {
	discountBps uint16
	tier        uint8
	cachedAt    time.Time
}

// SovereigntyProvider is an interface for querying sovereignty data.
// In production this calls the SovereigntyEngine contract via EVM;
// for tests a mock implementation is used.
type SovereigntyProvider interface {
	GetFeeDiscount(addr common.Address) (discountBps uint16, tier uint8, err error)
}

// NewDynamicFeeModel creates a fee model starting at 1 gwei.
func NewDynamicFeeModel() *DynamicFeeModel {
	return &DynamicFeeModel{
		baseFee:        big.NewInt(1_000_000_000), // 1 gwei starting base fee
		minBaseFee:     big.NewInt(100_000_000),    // 0.1 gwei floor
		maxBaseFee:     big.NewInt(100_000_000_000), // 100 gwei ceiling
		targetGasUsage: 0.5,
		emaGasUsage:    0.5,
		emaAlpha:       0.125, // EIP-1559 style
		discountCache:  make(map[common.Address]discountEntry),
		discountTTL:    60 * time.Second,
		logger:         log.New("module", "dynamic-fees"),
	}
}

// BaseFee returns the current base fee.
func (f *DynamicFeeModel) BaseFee() *big.Int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return new(big.Int).Set(f.baseFee)
}

// EffectiveFee returns the discounted fee for a specific sender.
// Returns (baseFee * (10000 - discountBps)) / 10000.
func (f *DynamicFeeModel) EffectiveFee(sender common.Address, provider SovereigntyProvider) *big.Int {
	base := f.BaseFee()

	if provider == nil {
		return base
	}

	discountBps, _, _ := f.getCachedDiscount(sender, provider)
	if discountBps == 0 {
		return base
	}

	// effective = baseFee * (10000 - discount) / 10000
	multiplier := new(big.Int).SetUint64(10000 - uint64(discountBps))
	effective := new(big.Int).Mul(base, multiplier)
	effective.Div(effective, big.NewInt(10000))

	// Never go below minBaseFee
	if effective.Cmp(f.minBaseFee) < 0 {
		return new(big.Int).Set(f.minBaseFee)
	}
	return effective
}

// AdjustAfterBlock updates the base fee based on the gas utilization of
// the latest block. Called by the producer after each block.
func (f *DynamicFeeModel) AdjustAfterBlock(gasUsed uint64, gasLimit uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if gasLimit == 0 {
		return
	}

	utilization := float64(gasUsed) / float64(gasLimit)

	// Update EMA
	f.emaGasUsage = f.emaAlpha*utilization + (1-f.emaAlpha)*f.emaGasUsage

	// Compute adjustment: if utilization > target, increase; otherwise decrease
	// Max change per block: 12.5% (like EIP-1559)
	delta := (f.emaGasUsage - f.targetGasUsage) / f.targetGasUsage

	// Clamp delta
	if delta > 0.125 {
		delta = 0.125
	} else if delta < -0.125 {
		delta = -0.125
	}

	// adjustment = baseFee * delta
	// Use integer math: multiply then divide by 8 (12.5% = 1/8)
	adjustment := new(big.Int).Set(f.baseFee)
	if delta > 0 {
		// Increase: baseFee += baseFee * |delta|
		pct := int64(delta * 10000)
		adjustment.Mul(adjustment, big.NewInt(pct))
		adjustment.Div(adjustment, big.NewInt(10000))
		f.baseFee.Add(f.baseFee, adjustment)
	} else if delta < 0 {
		// Decrease: baseFee -= baseFee * |delta|
		pct := int64(-delta * 10000)
		adjustment.Mul(adjustment, big.NewInt(pct))
		adjustment.Div(adjustment, big.NewInt(10000))
		f.baseFee.Sub(f.baseFee, adjustment)
	}

	// Enforce bounds
	if f.baseFee.Cmp(f.minBaseFee) < 0 {
		f.baseFee.Set(f.minBaseFee)
	}
	if f.baseFee.Cmp(f.maxBaseFee) > 0 {
		f.baseFee.Set(f.maxBaseFee)
	}

	f.logger.Debug("Base fee adjusted",
		"baseFee", f.baseFee,
		"emaGasUsage", f.emaGasUsage,
		"blockGasUsed", gasUsed,
		"blockGasLimit", gasLimit,
	)
}

// getCachedDiscount returns the sovereignty discount for a sender,
// using a cache to avoid frequent contract calls.
func (f *DynamicFeeModel) getCachedDiscount(addr common.Address, provider SovereigntyProvider) (uint16, uint8, error) {
	f.mu.RLock()
	if entry, ok := f.discountCache[addr]; ok {
		if time.Since(entry.cachedAt) < f.discountTTL {
			f.mu.RUnlock()
			return entry.discountBps, entry.tier, nil
		}
	}
	f.mu.RUnlock()

	discountBps, tier, err := provider.GetFeeDiscount(addr)
	if err != nil {
		return 0, 0, err
	}

	f.mu.Lock()
	f.discountCache[addr] = discountEntry{
		discountBps: discountBps,
		tier:        tier,
		cachedAt:    time.Now(),
	}
	f.mu.Unlock()

	return discountBps, tier, nil
}

// GetDiscount returns the cached discount for display purposes.
func (f *DynamicFeeModel) GetDiscount(addr common.Address) (discountBps uint16, tier uint8) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	entry, ok := f.discountCache[addr]
	if !ok {
		return 0, 0
	}
	return entry.discountBps, entry.tier
}

// Stats returns fee model statistics.
func (f *DynamicFeeModel) Stats() FeeStats {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return FeeStats{
		BaseFeeWei:     new(big.Int).Set(f.baseFee),
		BaseFeeGwei:    new(big.Int).Div(new(big.Int).Set(f.baseFee), big.NewInt(1_000_000_000)).Uint64(),
		EMAGasUsage:    f.emaGasUsage,
		TargetGasUsage: f.targetGasUsage,
		CacheSize:      len(f.discountCache),
	}
}

// FeeStats contains fee model metrics.
type FeeStats struct {
	BaseFeeWei     *big.Int `json:"baseFeeWei"`
	BaseFeeGwei    uint64   `json:"baseFeeGwei"`
	EMAGasUsage    float64  `json:"emaGasUsage"`
	TargetGasUsage float64  `json:"targetGasUsage"`
	CacheSize      int      `json:"cacheSize"`
}
