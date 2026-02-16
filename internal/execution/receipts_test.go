package execution

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestCreateReceipt(t *testing.T) {
	tx := types.NewTransaction(
		0,
		common.HexToAddress("0xdead"),
		big.NewInt(1e18),
		21000,
		big.NewInt(1e9),
		[]byte("test calldata"),
	)

	ethReceipt := &types.Receipt{
		Status:  types.ReceiptStatusSuccessful,
		GasUsed: 21000,
		Logs:    []*types.Log{},
	}

	preState := common.HexToHash("0x1111")
	postState := common.HexToHash("0x2222")
	sender := common.HexToAddress("0xabc")

	cr := CreateReceipt(tx, ethReceipt, 1, 0, sender, preState, postState, nil)

	if cr.TxHash != tx.Hash() {
		t.Errorf("TxHash mismatch: got %s", cr.TxHash.Hex())
	}
	if cr.BlockNumber != 1 {
		t.Errorf("BlockNumber = %d, want 1", cr.BlockNumber)
	}
	if cr.Sender != sender {
		t.Errorf("Sender = %s, want %s", cr.Sender.Hex(), sender.Hex())
	}
	if cr.PreStateRoot != preState {
		t.Error("PreStateRoot mismatch")
	}
	if cr.PostStateRoot != postState {
		t.Error("PostStateRoot mismatch")
	}
	if cr.Status != types.ReceiptStatusSuccessful {
		t.Errorf("Status = %d, want 1", cr.Status)
	}
	if cr.GasUsed != 21000 {
		t.Errorf("GasUsed = %d, want 21000", cr.GasUsed)
	}
	if cr.ReceiptHash == (common.Hash{}) {
		t.Error("ReceiptHash should not be zero")
	}
}

func TestReceipt_Verify(t *testing.T) {
	tx := types.NewTransaction(0, common.HexToAddress("0xdead"), big.NewInt(0), 21000, big.NewInt(1e9), nil)
	ethReceipt := &types.Receipt{Status: 1, GasUsed: 21000, Logs: []*types.Log{}}

	cr := CreateReceipt(tx, ethReceipt, 1, 0, common.HexToAddress("0xabc"),
		common.HexToHash("0x1111"), common.HexToHash("0x2222"), nil)

	if !cr.Verify() {
		t.Error("fresh receipt should verify")
	}

	// Tamper with the receipt
	cr.GasUsed = 42000
	if cr.Verify() {
		t.Error("tampered receipt should NOT verify")
	}
}

func TestReceipt_Deterministic(t *testing.T) {
	tx := types.NewTransaction(0, common.HexToAddress("0xdead"), big.NewInt(0), 21000, big.NewInt(1e9), nil)
	ethReceipt := &types.Receipt{Status: 1, GasUsed: 21000, Logs: []*types.Log{}}

	cr1 := CreateReceipt(tx, ethReceipt, 1, 0, common.HexToAddress("0xabc"),
		common.HexToHash("0x1111"), common.HexToHash("0x2222"), nil)
	cr2 := CreateReceipt(tx, ethReceipt, 1, 0, common.HexToAddress("0xabc"),
		common.HexToHash("0x1111"), common.HexToHash("0x2222"), nil)

	if cr1.ReceiptHash != cr2.ReceiptHash {
		t.Error("same inputs should produce same receipt hash")
	}
}

func TestReceipt_DifferentInputsDifferentHash(t *testing.T) {
	tx1 := types.NewTransaction(0, common.HexToAddress("0xdead"), big.NewInt(0), 21000, big.NewInt(1e9), nil)
	tx2 := types.NewTransaction(1, common.HexToAddress("0xdead"), big.NewInt(0), 21000, big.NewInt(1e9), nil)
	ethReceipt := &types.Receipt{Status: 1, GasUsed: 21000, Logs: []*types.Log{}}

	cr1 := CreateReceipt(tx1, ethReceipt, 1, 0, common.HexToAddress("0xabc"),
		common.HexToHash("0x1111"), common.HexToHash("0x2222"), nil)
	cr2 := CreateReceipt(tx2, ethReceipt, 1, 1, common.HexToAddress("0xabc"),
		common.HexToHash("0x1111"), common.HexToHash("0x3333"), nil)

	if cr1.ReceiptHash == cr2.ReceiptHash {
		t.Error("different inputs should produce different receipt hashes")
	}
}

func TestReceiptStore_StoreAndGet(t *testing.T) {
	store := NewReceiptStore()

	tx := types.NewTransaction(0, common.HexToAddress("0xdead"), big.NewInt(0), 21000, big.NewInt(1e9), nil)
	ethReceipt := &types.Receipt{Status: 1, GasUsed: 21000, Logs: []*types.Log{}}
	cr := CreateReceipt(tx, ethReceipt, 1, 0, common.HexToAddress("0xabc"),
		common.HexToHash("0x1111"), common.HexToHash("0x2222"), nil)

	store.Store(cr)

	got := store.Get(tx.Hash())
	if got == nil {
		t.Fatal("receipt not found")
	}
	if got.ReceiptHash != cr.ReceiptHash {
		t.Error("receipt hash mismatch")
	}
	if store.Count() != 1 {
		t.Errorf("Count() = %d, want 1", store.Count())
	}
}

func TestReceiptStore_GetByBlock(t *testing.T) {
	store := NewReceiptStore()
	ethReceipt := &types.Receipt{Status: 1, GasUsed: 21000, Logs: []*types.Log{}}

	for i := 0; i < 5; i++ {
		tx := types.NewTransaction(uint64(i), common.HexToAddress("0xdead"), big.NewInt(0), 21000, big.NewInt(1e9), nil)
		cr := CreateReceipt(tx, ethReceipt, 42, i, common.HexToAddress("0xabc"),
			common.HexToHash("0x1111"), common.HexToHash("0x2222"), nil)
		store.Store(cr)
	}

	block42 := store.GetByBlock(42)
	if len(block42) != 5 {
		t.Errorf("GetByBlock(42) returned %d receipts, want 5", len(block42))
	}

	block99 := store.GetByBlock(99)
	if len(block99) != 0 {
		t.Errorf("GetByBlock(99) returned %d receipts, want 0", len(block99))
	}
}

func TestReceiptStore_GetNotFound(t *testing.T) {
	store := NewReceiptStore()
	got := store.Get(common.HexToHash("0xdeadbeef"))
	if got != nil {
		t.Error("should return nil for unknown tx")
	}
}

func TestReceiptStore_BlockReceiptRoot(t *testing.T) {
	store := NewReceiptStore()
	ethReceipt := &types.Receipt{Status: 1, GasUsed: 21000, Logs: []*types.Log{}}

	for i := 0; i < 4; i++ {
		tx := types.NewTransaction(uint64(i), common.HexToAddress("0xdead"), big.NewInt(0), 21000, big.NewInt(1e9), nil)
		cr := CreateReceipt(tx, ethReceipt, 1, i, common.HexToAddress("0xabc"),
			common.HexToHash("0x1111"), common.HexToHash("0x2222"), nil)
		store.Store(cr)
	}

	root := store.ComputeBlockReceiptRoot(1)
	if root == (common.Hash{}) {
		t.Error("block receipt root should not be zero")
	}

	// Same block should produce same root
	root2 := store.ComputeBlockReceiptRoot(1)
	if root != root2 {
		t.Error("block receipt root should be deterministic")
	}
}

func TestReceiptStore_EmptyBlockRoot(t *testing.T) {
	store := NewReceiptStore()
	root := store.ComputeBlockReceiptRoot(999)
	if root != (common.Hash{}) {
		t.Error("empty block should have zero root")
	}
}

func TestReceipt_WithLogs(t *testing.T) {
	tx := types.NewTransaction(0, common.HexToAddress("0xdead"), big.NewInt(0), 21000, big.NewInt(1e9), nil)
	ethReceipt := &types.Receipt{
		Status:  1,
		GasUsed: 50000,
		Logs: []*types.Log{
			{
				Address: common.HexToAddress("0xcontract"),
				Topics:  []common.Hash{common.HexToHash("0xevent1")},
				Data:    []byte("log data"),
			},
		},
	}

	cr := CreateReceipt(tx, ethReceipt, 1, 0, common.HexToAddress("0xabc"),
		common.HexToHash("0x1111"), common.HexToHash("0x2222"), []byte("return data"))

	if cr.LogsHash == (common.Hash{}) {
		t.Error("logs hash should not be zero when logs exist")
	}
	if cr.OutputHash == (common.Hash{}) {
		t.Error("output hash should not be zero when return data exists")
	}
	if !cr.Verify() {
		t.Error("receipt with logs should verify")
	}
}

func TestComputeLogsHash_Empty(t *testing.T) {
	h := computeLogsHash(nil)
	if h != (common.Hash{}) {
		t.Error("empty logs should produce zero hash")
	}
}

func TestComputeMerkleRoot_SingleHash(t *testing.T) {
	h := common.HexToHash("0x1234")
	root := computeMerkleRoot([]common.Hash{h})
	if root != h {
		t.Error("single hash should be its own root")
	}
}

func TestComputeMerkleRoot_Empty(t *testing.T) {
	root := computeMerkleRoot(nil)
	if root != (common.Hash{}) {
		t.Error("empty should produce zero root")
	}
}
