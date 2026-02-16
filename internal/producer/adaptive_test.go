package producer

import (
	"math"
	"testing"
)

func TestAdaptiveBlockSizer_ExpandOnHighUtil(t *testing.T) {
	cfg := DefaultAdaptiveSizerConfig()
	sizer := NewAdaptiveBlockSizer(cfg)

	initial := sizer.CurrentGasLimit()

	// Simulate high utilization blocks (90% gas used)
	for i := 0; i < 20; i++ {
		sizer.AdjustAfterBlock(uint64(float64(sizer.CurrentGasLimit())*0.9), sizer.CurrentGasLimit())
	}

	if sizer.CurrentGasLimit() <= initial {
		t.Errorf("gas limit should expand: got %d, initial was %d", sizer.CurrentGasLimit(), initial)
	}
}

func TestAdaptiveBlockSizer_ShrinkOnLowUtil(t *testing.T) {
	cfg := DefaultAdaptiveSizerConfig()
	sizer := NewAdaptiveBlockSizer(cfg)

	initial := sizer.CurrentGasLimit()

	// Simulate low utilization blocks (10% gas used)
	for i := 0; i < 20; i++ {
		sizer.AdjustAfterBlock(uint64(float64(sizer.CurrentGasLimit())*0.1), sizer.CurrentGasLimit())
	}

	if sizer.CurrentGasLimit() >= initial {
		t.Errorf("gas limit should shrink: got %d, initial was %d", sizer.CurrentGasLimit(), initial)
	}
}

func TestAdaptiveBlockSizer_BoundsRespected(t *testing.T) {
	cfg := DefaultAdaptiveSizerConfig()
	sizer := NewAdaptiveBlockSizer(cfg)

	// Very high utilization for many blocks — should hit ceiling
	for i := 0; i < 100; i++ {
		sizer.AdjustAfterBlock(sizer.CurrentGasLimit(), sizer.CurrentGasLimit()) // 100% used
	}

	if sizer.CurrentGasLimit() > cfg.MaxGasLimit {
		t.Errorf("gas limit exceeded max: got %d, max %d", sizer.CurrentGasLimit(), cfg.MaxGasLimit)
	}
	if sizer.CurrentMaxTx() > cfg.MaxMaxTx {
		t.Errorf("maxTx exceeded max: got %d, max %d", sizer.CurrentMaxTx(), cfg.MaxMaxTx)
	}

	// Very low utilization for many blocks — should hit floor
	for i := 0; i < 100; i++ {
		sizer.AdjustAfterBlock(0, sizer.CurrentGasLimit()) // 0% used
	}

	if sizer.CurrentGasLimit() < cfg.MinGasLimit {
		t.Errorf("gas limit below min: got %d, min %d", sizer.CurrentGasLimit(), cfg.MinGasLimit)
	}
	if sizer.CurrentMaxTx() < cfg.MinMaxTx {
		t.Errorf("maxTx below min: got %d, min %d", sizer.CurrentMaxTx(), cfg.MinMaxTx)
	}
}

func TestAdaptiveBlockSizer_StableAtTarget(t *testing.T) {
	cfg := DefaultAdaptiveSizerConfig()
	sizer := NewAdaptiveBlockSizer(cfg)

	initial := sizer.CurrentGasLimit()

	// Simulate target utilization — should stay stable
	for i := 0; i < 50; i++ {
		gasUsed := uint64(float64(sizer.CurrentGasLimit()) * cfg.TargetUtilization)
		sizer.AdjustAfterBlock(gasUsed, sizer.CurrentGasLimit())
	}

	// Should remain close to initial
	delta := math.Abs(float64(sizer.CurrentGasLimit()) - float64(initial))
	maxDelta := float64(initial) * 0.15 // ±15% tolerance
	if delta > maxDelta {
		t.Errorf("gas limit drifted too much at target util: got %d, initial %d, delta %f",
			sizer.CurrentGasLimit(), initial, delta)
	}
}

func TestAdaptiveBlockSizer_Stats(t *testing.T) {
	cfg := DefaultAdaptiveSizerConfig()
	sizer := NewAdaptiveBlockSizer(cfg)

	gasLimit, maxTx, util := sizer.Stats()
	if gasLimit != cfg.InitialGasLimit {
		t.Errorf("initial gas limit = %d, want %d", gasLimit, cfg.InitialGasLimit)
	}
	if maxTx != cfg.InitialMaxTx {
		t.Errorf("initial maxTx = %d, want %d", maxTx, cfg.InitialMaxTx)
	}
	if util != cfg.TargetUtilization {
		t.Errorf("initial utilization = %f, want %f", util, cfg.TargetUtilization)
	}
}

func TestAdaptiveBlockSizer_ZeroGasLimit(t *testing.T) {
	cfg := DefaultAdaptiveSizerConfig()
	sizer := NewAdaptiveBlockSizer(cfg)

	initial := sizer.CurrentGasLimit()
	sizer.AdjustAfterBlock(0, 0) // edge case
	if sizer.CurrentGasLimit() != initial {
		t.Error("zero gas limit should not cause adjustment")
	}
}

func TestAdaptiveBlockSizer_MaxTxAdjusts(t *testing.T) {
	cfg := DefaultAdaptiveSizerConfig()
	sizer := NewAdaptiveBlockSizer(cfg)

	initial := sizer.CurrentMaxTx()

	// High utilization
	for i := 0; i < 20; i++ {
		sizer.AdjustAfterBlock(sizer.CurrentGasLimit(), sizer.CurrentGasLimit())
	}

	if sizer.CurrentMaxTx() <= initial {
		t.Errorf("maxTx should expand: got %d, initial %d", sizer.CurrentMaxTx(), initial)
	}
}
