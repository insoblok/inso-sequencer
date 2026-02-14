package fees

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

// mockSovereigntyProvider implements SovereigntyProvider for testing.
type mockSovereigntyProvider struct {
	discounts map[common.Address]struct {
		bps  uint16
		tier uint8
	}
}

func (m *mockSovereigntyProvider) GetFeeDiscount(addr common.Address) (uint16, uint8, error) {
	if entry, ok := m.discounts[addr]; ok {
		return entry.bps, entry.tier, nil
	}
	return 0, 0, nil
}

func TestNewDynamicFeeModel(t *testing.T) {
	fm := NewDynamicFeeModel()
	if fm.BaseFee().Cmp(big.NewInt(1_000_000_000)) != 0 {
		t.Fatalf("expected 1 gwei starting base fee, got %s", fm.BaseFee())
	}
}

func TestBaseFeeIncreasesOnHighUtilization(t *testing.T) {
	fm := NewDynamicFeeModel()
	initial := fm.BaseFee()

	// Simulate 100% full blocks
	for i := 0; i < 10; i++ {
		fm.AdjustAfterBlock(30_000_000, 30_000_000)
	}

	if fm.BaseFee().Cmp(initial) <= 0 {
		t.Fatal("base fee should increase on high utilization")
	}
}

func TestBaseFeeDecreasesOnLowUtilization(t *testing.T) {
	fm := NewDynamicFeeModel()
	initial := fm.BaseFee()

	// Simulate empty blocks
	for i := 0; i < 10; i++ {
		fm.AdjustAfterBlock(0, 30_000_000)
	}

	if fm.BaseFee().Cmp(initial) >= 0 {
		t.Fatal("base fee should decrease on low utilization")
	}
}

func TestBaseFeeFloor(t *testing.T) {
	fm := NewDynamicFeeModel()

	// Drive fee very low
	for i := 0; i < 1000; i++ {
		fm.AdjustAfterBlock(0, 30_000_000)
	}

	if fm.BaseFee().Cmp(fm.minBaseFee) < 0 {
		t.Fatalf("base fee %s below floor %s", fm.BaseFee(), fm.minBaseFee)
	}
}

func TestBaseFeeStableAt50Percent(t *testing.T) {
	fm := NewDynamicFeeModel()

	// 50% utilization should keep fee roughly stable
	for i := 0; i < 20; i++ {
		fm.AdjustAfterBlock(15_000_000, 30_000_000)
	}

	diff := new(big.Int).Sub(fm.BaseFee(), big.NewInt(1_000_000_000))
	if diff.Abs(diff).Cmp(big.NewInt(100_000_000)) > 0 { // within 0.1 gwei
		t.Fatalf("base fee should be stable near 1 gwei at 50%% utilization, got %s", fm.BaseFee())
	}
}

func TestEffectiveFeeWithNoProvider(t *testing.T) {
	fm := NewDynamicFeeModel()
	addr := common.HexToAddress("0x1234")

	fee := fm.EffectiveFee(addr, nil)
	if fee.Cmp(fm.BaseFee()) != 0 {
		t.Fatal("effective fee should equal base fee when no provider")
	}
}

func TestEffectiveFeeWithDiscount(t *testing.T) {
	fm := NewDynamicFeeModel()
	addr := common.HexToAddress("0x1234")

	provider := &mockSovereigntyProvider{
		discounts: map[common.Address]struct {
			bps  uint16
			tier uint8
		}{
			addr: {bps: 2500, tier: 3}, // Gold: 25% off
		},
	}

	effective := fm.EffectiveFee(addr, provider)
	expected := big.NewInt(750_000_000) // 1 gwei * 75% = 0.75 gwei

	if effective.Cmp(expected) != 0 {
		t.Fatalf("expected %s, got %s", expected, effective)
	}
}

func TestEffectiveFeeNoDiscount(t *testing.T) {
	fm := NewDynamicFeeModel()
	addr := common.HexToAddress("0xabcd")

	provider := &mockSovereigntyProvider{
		discounts: map[common.Address]struct {
			bps  uint16
			tier uint8
		}{},
	}

	effective := fm.EffectiveFee(addr, provider)
	if effective.Cmp(fm.BaseFee()) != 0 {
		t.Fatal("effective fee should equal base fee for non-sovereign address")
	}
}

func TestGetDiscountCached(t *testing.T) {
	fm := NewDynamicFeeModel()
	addr := common.HexToAddress("0x1234")

	provider := &mockSovereigntyProvider{
		discounts: map[common.Address]struct {
			bps  uint16
			tier uint8
		}{
			addr: {bps: 4000, tier: 4}, // Platinum
		},
	}

	// First call populates cache
	fm.EffectiveFee(addr, provider)

	bps, tier := fm.GetDiscount(addr)
	if bps != 4000 || tier != 4 {
		t.Fatalf("expected 4000 bps tier 4, got %d bps tier %d", bps, tier)
	}
}

func TestFeeStats(t *testing.T) {
	fm := NewDynamicFeeModel()
	stats := fm.Stats()

	if stats.BaseFeeGwei != 1 {
		t.Fatalf("expected 1 gwei, got %d", stats.BaseFeeGwei)
	}
	if stats.TargetGasUsage != 0.5 {
		t.Fatalf("expected 0.5 target, got %f", stats.TargetGasUsage)
	}
}
