package batcher

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rlp"
)

// ── L1Client tests ───────────────────────────────────────────────────────────

func TestSimulatedL1Client_SendBatchTx(t *testing.T) {
	client := NewSimulatedL1Client()
	ctx := context.Background()

	data := []byte("test batch data")
	hash, err := client.SendBatchTx(ctx, data)
	if err != nil {
		t.Fatalf("SendBatchTx failed: %v", err)
	}
	if hash == (common.Hash{}) {
		t.Fatal("Expected non-zero tx hash")
	}

	// Second call should produce a different hash
	hash2, err := client.SendBatchTx(ctx, data)
	if err != nil {
		t.Fatalf("Second SendBatchTx failed: %v", err)
	}
	if hash == hash2 {
		t.Fatal("Expected different hashes for sequential calls")
	}
}

func TestSimulatedL1Client_LatestL1Block(t *testing.T) {
	client := NewSimulatedL1Client()
	ctx := context.Background()

	num1, hash1, err := client.LatestL1Block(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if num1 == 0 {
		t.Fatal("Expected non-zero block number")
	}
	if hash1 == (common.Hash{}) {
		t.Fatal("Expected non-zero block hash")
	}

	num2, _, err := client.LatestL1Block(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if num2 <= num1 {
		t.Fatalf("Block number should increase: %d <= %d", num2, num1)
	}
}

func TestSimulatedL1Client_IsConfirmed(t *testing.T) {
	client := NewSimulatedL1Client()
	ctx := context.Background()

	confirmed, err := client.IsConfirmed(ctx, common.Hash{}, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !confirmed {
		t.Fatal("Simulated client should always confirm")
	}
}

// ── Compression tests ────────────────────────────────────────────────────────

func TestCompressDecompress(t *testing.T) {
	original := []byte("This is test data for compression. It should compress well with repeated patterns.")

	compressed, err := compressData(original)
	if err != nil {
		t.Fatalf("Compress failed: %v", err)
	}

	decompressed, err := DecompressData(compressed)
	if err != nil {
		t.Fatalf("Decompress failed: %v", err)
	}

	if !bytes.Equal(original, decompressed) {
		t.Fatal("Decompressed data doesn't match original")
	}
}

func TestCompressRatio(t *testing.T) {
	// Highly compressible data (lots of zeros)
	data := make([]byte, 10000)
	compressed, err := compressData(data)
	if err != nil {
		t.Fatal(err)
	}

	ratio := float64(len(data)) / float64(len(compressed))
	if ratio < 2.0 {
		t.Fatalf("Expected good compression ratio for zeros, got %.1fx", ratio)
	}
	t.Logf("Compression ratio: %.1fx (%d -> %d bytes)", ratio, len(data), len(compressed))
}

// ── Batch frame tests ────────────────────────────────────────────────────────

func TestBatchFrameRLPRoundtrip(t *testing.T) {
	frame := &BatchFrame{
		Version:    1,
		ChainID:    42069,
		StartBlock: 1,
		EndBlock:   3,
		Timestamp:  uint64(time.Now().Unix()),
		Blocks: []BlockData{
			{
				Number:     1,
				Hash:       common.HexToHash("0xabc"),
				ParentHash: common.Hash{},
				Timestamp:  1000,
				StateRoot:  common.HexToHash("0xdef"),
				GasUsed:    21000,
				TxCount:    1,
				TxData:     [][]byte{{0x01, 0x02}},
			},
			{
				Number:     2,
				Hash:       common.HexToHash("0x123"),
				ParentHash: common.HexToHash("0xabc"),
				Timestamp:  1001,
				StateRoot:  common.HexToHash("0x456"),
				GasUsed:    42000,
				TxCount:    2,
				TxData:     [][]byte{{0x03, 0x04}, {0x05, 0x06}},
			},
		},
	}

	// Encode
	encoded, err := rlp.EncodeToBytes(frame)
	if err != nil {
		t.Fatalf("RLP encode failed: %v", err)
	}

	// Decode
	var decoded BatchFrame
	if err := rlp.DecodeBytes(encoded, &decoded); err != nil {
		t.Fatalf("RLP decode failed: %v", err)
	}

	// Verify
	if decoded.Version != frame.Version {
		t.Errorf("Version mismatch: %d != %d", decoded.Version, frame.Version)
	}
	if decoded.ChainID != frame.ChainID {
		t.Errorf("ChainID mismatch: %d != %d", decoded.ChainID, frame.ChainID)
	}
	if decoded.StartBlock != frame.StartBlock {
		t.Errorf("StartBlock mismatch: %d != %d", decoded.StartBlock, frame.StartBlock)
	}
	if decoded.EndBlock != frame.EndBlock {
		t.Errorf("EndBlock mismatch: %d != %d", decoded.EndBlock, frame.EndBlock)
	}
	if len(decoded.Blocks) != len(frame.Blocks) {
		t.Fatalf("Block count mismatch: %d != %d", len(decoded.Blocks), len(frame.Blocks))
	}
	for i, b := range decoded.Blocks {
		if b.Number != frame.Blocks[i].Number {
			t.Errorf("Block %d number mismatch", i)
		}
		if b.Hash != frame.Blocks[i].Hash {
			t.Errorf("Block %d hash mismatch", i)
		}
		if b.GasUsed != frame.Blocks[i].GasUsed {
			t.Errorf("Block %d gasUsed mismatch", i)
		}
		if len(b.TxData) != len(frame.Blocks[i].TxData) {
			t.Errorf("Block %d txData count mismatch", i)
		}
	}

	t.Logf("Batch frame RLP roundtrip: %d blocks, %d bytes encoded", len(frame.Blocks), len(encoded))
}

func TestBatchFrameCompressedRoundtrip(t *testing.T) {
	frame := &BatchFrame{
		Version:    1,
		ChainID:    42069,
		StartBlock: 1,
		EndBlock:   10,
		Timestamp:  uint64(time.Now().Unix()),
		Blocks:     make([]BlockData, 10),
	}

	for i := 0; i < 10; i++ {
		frame.Blocks[i] = BlockData{
			Number:    uint64(i + 1),
			Hash:      common.BigToHash(common.Big1),
			Timestamp: uint64(1000 + i),
			StateRoot: common.BigToHash(common.Big2),
			GasUsed:   21000 * uint64(i+1),
			TxCount:   uint64(i),
		}
	}

	// Encode + compress
	encoded, err := rlp.EncodeToBytes(frame)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := compressData(encoded)
	if err != nil {
		t.Fatal(err)
	}

	// Decompress + decode
	decompressed, err := DecompressData(compressed)
	if err != nil {
		t.Fatal(err)
	}
	var decoded BatchFrame
	if err := rlp.DecodeBytes(decompressed, &decoded); err != nil {
		t.Fatal(err)
	}

	if decoded.StartBlock != frame.StartBlock || decoded.EndBlock != frame.EndBlock {
		t.Fatal("Frame range mismatch after roundtrip")
	}
	if len(decoded.Blocks) != 10 {
		t.Fatalf("Expected 10 blocks, got %d", len(decoded.Blocks))
	}

	ratio := float64(len(encoded)) / float64(len(compressed))
	t.Logf("Full roundtrip: %d bytes encoded, %d compressed (%.1fx)", len(encoded), len(compressed), ratio)
}
