package mempool

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestClassifyLane(t *testing.T) {
	tests := []struct {
		name       string
		tasteScore float64
		wantLane   Lane
	}{
		{"Platinum user → fast", 0.90, LaneFast},
		{"Gold user → fast", 0.70, LaneFast},
		{"Gold threshold → fast", 0.65, LaneFast},
		{"Silver user → standard", 0.45, LaneStandard},
		{"Bronze user → standard", 0.20, LaneStandard},
		{"Bronze threshold → standard", 0.15, LaneStandard},
		{"Low score → slow", 0.10, LaneSlow},
		{"No score → slow", 0.0, LaneSlow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyLane(tt.tasteScore)
			if got != tt.wantLane {
				t.Errorf("ClassifyLane(%v) = %v, want %v", tt.tasteScore, got, tt.wantLane)
			}
		})
	}
}

func TestLanedMempool_Add(t *testing.T) {
	lm := NewLanedMempool(0.7, 1000)

	tx1 := makeTx(1, 21000)
	tx2 := makeTx(2, 21000)
	tx3 := makeTx(3, 21000)

	// Add to different lanes
	if err := lm.Add(tx1, common.HexToAddress("0x1"), 0.90); err != nil { // fast
		t.Fatalf("Add fast: %v", err)
	}
	if err := lm.Add(tx2, common.HexToAddress("0x2"), 0.40); err != nil { // standard
		t.Fatalf("Add standard: %v", err)
	}
	if err := lm.Add(tx3, common.HexToAddress("0x3"), 0.05); err != nil { // slow
		t.Fatalf("Add slow: %v", err)
	}

	fast, std, slow := lm.LaneSizes()
	if fast != 1 || std != 1 || slow != 1 {
		t.Errorf("LaneSizes() = (%d, %d, %d), want (1, 1, 1)", fast, std, slow)
	}

	if lm.Len() != 3 {
		t.Errorf("Len() = %d, want 3", lm.Len())
	}
}

func TestLanedMempool_DuplicateRejected(t *testing.T) {
	lm := NewLanedMempool(0.7, 1000)

	tx := makeTx(1, 21000)
	_ = lm.Add(tx, common.HexToAddress("0x1"), 0.90)

	err := lm.Add(tx, common.HexToAddress("0x1"), 0.90)
	if err != ErrAlreadyKnown {
		t.Errorf("expected ErrAlreadyKnown, got %v", err)
	}
}

func TestLanedMempool_Full(t *testing.T) {
	lm := NewLanedMempool(0.7, 2) // max 2

	_ = lm.Add(makeTx(1, 21000), common.HexToAddress("0x1"), 0.90)
	_ = lm.Add(makeTx(2, 21000), common.HexToAddress("0x2"), 0.40)

	err := lm.Add(makeTx(3, 21000), common.HexToAddress("0x3"), 0.05)
	if err != ErrMempoolFull {
		t.Errorf("expected ErrMempoolFull, got %v", err)
	}
}

func TestLanedMempool_PopBatchLaned(t *testing.T) {
	lm := NewLanedMempool(0.7, 1000)

	// Add 3 txs to fast lane, 2 to standard, 1 to slow
	for i := 0; i < 3; i++ {
		_ = lm.Add(makeTx(uint64(i), 21000), common.HexToAddress("0x1"), 0.90)
	}
	for i := 3; i < 5; i++ {
		_ = lm.Add(makeTx(uint64(i), 21000), common.HexToAddress("0x2"), 0.40)
	}
	_ = lm.Add(makeTx(5, 21000), common.HexToAddress("0x3"), 0.05)

	// Pop with enough gas for all
	batch := lm.PopBatchLaned(100, 200000) // 21k * 6 = 126k < 200k

	if len(batch) != 6 {
		t.Errorf("PopBatchLaned got %d txs, want 6", len(batch))
	}

	if lm.Len() != 0 {
		t.Errorf("Len() after drain = %d, want 0", lm.Len())
	}
}

func TestLanedMempool_GasAllocation(t *testing.T) {
	lm := NewLanedMempool(0.7, 1000)

	// Add many txs to slow lane only
	for i := 0; i < 10; i++ {
		_ = lm.Add(makeTx(uint64(i), 21000), common.HexToAddress("0x3"), 0.05)
	}

	// Gas limit = 63000 (3 txs worth). Slow lane gets 20% = 12600 gas (0 txs at 21k each)
	// But Phase 2 fills remaining from slow, so we should get up to 3 txs
	batch := lm.PopBatchLaned(100, 63000)

	// With 63k gas: slow allocation = 12600 (0 txs), but phase 2 fills remaining
	if len(batch) != 3 {
		t.Errorf("expected 3 txs (gas-limited), got %d", len(batch))
	}
}

func TestLanedMempool_Has(t *testing.T) {
	lm := NewLanedMempool(0.7, 1000)
	tx := makeTx(1, 21000)

	if lm.Has(tx.Hash()) {
		t.Error("Has should be false before Add")
	}

	_ = lm.Add(tx, common.HexToAddress("0x1"), 0.50)

	if !lm.Has(tx.Hash()) {
		t.Error("Has should be true after Add")
	}
}

func TestLanedMempool_Reset(t *testing.T) {
	lm := NewLanedMempool(0.7, 1000)
	_ = lm.Add(makeTx(1, 21000), common.HexToAddress("0x1"), 0.90)
	_ = lm.Add(makeTx(2, 21000), common.HexToAddress("0x2"), 0.40)

	lm.Reset()

	if lm.Len() != 0 {
		t.Errorf("Len() after Reset = %d, want 0", lm.Len())
	}
}

// makeTx creates a simple legacy transaction for testing.
func makeTx(nonce uint64, gas uint64) *types.Transaction {
	return types.NewTransaction(
		nonce,
		common.HexToAddress("0xdead"),
		big.NewInt(0),
		gas,
		big.NewInt(1e9), // 1 gwei
		nil,
	)
}
