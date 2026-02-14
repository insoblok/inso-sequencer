package execution

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/types"

	insoTypes "github.com/insoblok/inso-sequencer/pkg/types"
)

func newTestChainDB(t *testing.T) *ChainDB {
	t.Helper()
	db := rawdb.NewMemoryDatabase()
	return NewChainDB(db)
}

func TestChainDB_WriteAndReadBlock(t *testing.T) {
	cdb := newTestChainDB(t)

	header := &insoTypes.L2BlockHeader{
		Number:     1,
		Hash:       common.HexToHash("0x1111"),
		ParentHash: common.Hash{},
		Timestamp:  1000,
		StateRoot:  common.HexToHash("0x2222"),
		GasUsed:    21000,
		GasLimit:   30000000,
		TxCount:    0,
		BaseFee:    big.NewInt(1_000_000_000),
	}

	block := &insoTypes.L2Block{
		Header:       header,
		Transactions: []*types.Transaction{},
		Receipts:     []*types.Receipt{},
	}

	if err := cdb.WriteBlock(block); err != nil {
		t.Fatalf("failed to write block: %v", err)
	}

	// Read by number
	got, err := cdb.ReadBlock(1)
	if err != nil {
		t.Fatalf("failed to read block: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil block")
	}
	if got.Header.Number != 1 {
		t.Errorf("expected block number 1, got %d", got.Header.Number)
	}
	if got.Header.Hash != header.Hash {
		t.Errorf("hash mismatch: expected %s, got %s", header.Hash.Hex(), got.Header.Hash.Hex())
	}

	// Read by hash
	gotByHash, err := cdb.ReadBlockByHash(header.Hash)
	if err != nil {
		t.Fatalf("failed to read block by hash: %v", err)
	}
	if gotByHash == nil {
		t.Fatal("expected non-nil block by hash")
	}
	if gotByHash.Header.Number != 1 {
		t.Errorf("expected block number 1, got %d", gotByHash.Header.Number)
	}

	// Current block should be updated
	if cdb.CurrentBlock() != 1 {
		t.Errorf("expected current block 1, got %d", cdb.CurrentBlock())
	}

	// State root should be updated
	if cdb.LatestStateRoot() != header.StateRoot {
		t.Errorf("state root mismatch: expected %s, got %s",
			header.StateRoot.Hex(), cdb.LatestStateRoot().Hex())
	}
}

func TestChainDB_ReadBlockHash(t *testing.T) {
	cdb := newTestChainDB(t)

	header := &insoTypes.L2BlockHeader{
		Number:  5,
		Hash:    common.HexToHash("0x5555"),
		BaseFee: big.NewInt(1_000_000_000),
	}

	block := &insoTypes.L2Block{
		Header:       header,
		Transactions: []*types.Transaction{},
		Receipts:     []*types.Receipt{},
	}
	_ = cdb.WriteBlock(block)

	hash := cdb.ReadBlockHash(5)
	if hash != header.Hash {
		t.Errorf("expected hash %s, got %s", header.Hash.Hex(), hash.Hex())
	}

	// Non-existent block returns empty hash
	empty := cdb.ReadBlockHash(999)
	if empty != (common.Hash{}) {
		t.Errorf("expected empty hash for non-existent block, got %s", empty.Hex())
	}
}

func TestChainDB_GenesisRoot(t *testing.T) {
	cdb := newTestChainDB(t)

	root := common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890")

	if err := cdb.WriteGenesisRoot(root); err != nil {
		t.Fatalf("failed to write genesis root: %v", err)
	}

	got, err := cdb.ReadGenesisRoot()
	if err != nil {
		t.Fatalf("failed to read genesis root: %v", err)
	}
	if got != root {
		t.Errorf("expected %s, got %s", root.Hex(), got.Hex())
	}
}

func TestChainDB_WriteBatchAndRead(t *testing.T) {
	cdb := newTestChainDB(t)

	batch := &insoTypes.Batch{
		Index:      1,
		StartBlock: 1,
		EndBlock:   10,
		BlockCount: 10,
		L1TxHash:   common.HexToHash("0xbatch1"),
		SizeBytes:  10240,
		Status:     insoTypes.BatchSubmitted,
	}

	if err := cdb.WriteBatch(batch); err != nil {
		t.Fatalf("failed to write batch: %v", err)
	}

	// Read by index
	got, err := cdb.ReadBatch(1)
	if err != nil {
		t.Fatalf("failed to read batch: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil batch")
	}
	if got.Index != 1 {
		t.Errorf("expected batch index 1, got %d", got.Index)
	}
	if got.EndBlock != 10 {
		t.Errorf("expected end block 10, got %d", got.EndBlock)
	}

	// Read latest batch
	latest, err := cdb.ReadLatestBatch()
	if err != nil {
		t.Fatalf("failed to read latest batch: %v", err)
	}
	if latest == nil {
		t.Fatal("expected non-nil latest batch")
	}
	if latest.Index != 1 {
		t.Errorf("expected latest batch index 1, got %d", latest.Index)
	}
}

func TestChainDB_L1Origin(t *testing.T) {
	cdb := newTestChainDB(t)

	origin := &insoTypes.L1Origin{
		BlockNumber: 12345,
		BlockHash:   common.HexToHash("0xl1origin"),
		Timestamp:   1000000,
	}

	if err := cdb.WriteL1Origin(origin); err != nil {
		t.Fatalf("failed to write L1 origin: %v", err)
	}

	got, err := cdb.ReadL1Origin()
	if err != nil {
		t.Fatalf("failed to read L1 origin: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil L1 origin")
	}
	if got.BlockNumber != 12345 {
		t.Errorf("expected block number 12345, got %d", got.BlockNumber)
	}
}
