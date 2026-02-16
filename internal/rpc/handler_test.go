package rpc

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-sequencer/internal/fees"
)

// newTestHandler creates a minimal Handler for testing endpoints that
// do not require a full state manager.
func newTestHandler() *Handler {
	fm := fees.NewDynamicFeeModel()
	return &Handler{
		feeModel: fm,
		chainID:  big.NewInt(42069),
		logger:   log.New("module", "test"),
	}
}

// ── Phase 6+ endpoint tests ─────────────────────────────────────────────────

func TestGetCrossChainAttestation(t *testing.T) {
	h := newTestHandler()
	params := json.RawMessage(`["0xdeadbeef"]`)
	result, err := h.getCrossChainAttestation(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]interface{})
	if m["txHash"] != "0xdeadbeef" {
		t.Errorf("expected txHash=0xdeadbeef, got %v", m["txHash"])
	}
	if m["confirmed"] != true {
		t.Error("expected confirmed=true")
	}
	if m["sourceChain"].(uint64) != 42069 {
		t.Errorf("expected sourceChain=42069, got %v", m["sourceChain"])
	}
}

func TestGetCrossChainAttestationMissingParam(t *testing.T) {
	h := newTestHandler()
	params := json.RawMessage(`[]`)
	_, err := h.getCrossChainAttestation(params)
	if err == nil {
		t.Error("expected error for missing param")
	}
}

func TestGetOrderingProof(t *testing.T) {
	h := newTestHandler()
	params := json.RawMessage(`["0xabc123"]`)
	result, err := h.getOrderingProof(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]interface{})
	if m["txHash"] != "0xabc123" {
		t.Errorf("expected txHash=0xabc123, got %v", m["txHash"])
	}
	if m["orderingMethod"] != "tastescore" {
		t.Errorf("expected orderingMethod=tastescore, got %v", m["orderingMethod"])
	}
	if m["verified"] != true {
		t.Error("expected verified=true")
	}
}

func TestGetOrderingProofMissingParam(t *testing.T) {
	h := newTestHandler()
	_, err := h.getOrderingProof(json.RawMessage(`[]`))
	if err == nil {
		t.Error("expected error for missing param")
	}
}

func TestGetAIScorePreviewNoTasteScore(t *testing.T) {
	h := newTestHandler()
	params := json.RawMessage(`["0x1234567890abcdef1234567890abcdef12345678"]`)
	result, err := h.getAIScorePreview(params)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]interface{})
	if m["tasteScore"].(float64) != 0.5 {
		t.Errorf("expected default tasteScore=0.5, got %v", m["tasteScore"])
	}
	if m["aiModel"] != "tastescore-v1" {
		t.Errorf("expected aiModel=tastescore-v1, got %v", m["aiModel"])
	}
}

func TestGetAIScorePreviewMissingParam(t *testing.T) {
	h := newTestHandler()
	_, err := h.getAIScorePreview(json.RawMessage(`[]`))
	if err == nil {
		t.Error("expected error for missing param")
	}
}

func TestGetGovernanceProposals(t *testing.T) {
	h := newTestHandler()
	result, err := h.getGovernanceProposals()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]interface{})
	if m["totalCount"].(int) != 0 {
		t.Errorf("expected 0 proposals, got %v", m["totalCount"])
	}
	if m["quorumPercent"].(int) != 51 {
		t.Errorf("expected quorum=51, got %v", m["quorumPercent"])
	}
}

func TestGetRestakingStats(t *testing.T) {
	h := newTestHandler()
	result, err := h.getRestakingStats()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]interface{})
	if m["totalRestaked"].(string) != "0" {
		t.Errorf("expected totalRestaked=0, got %v", m["totalRestaked"])
	}
	if m["activeRestakers"].(int) != 0 {
		t.Errorf("expected 0 restakers, got %v", m["activeRestakers"])
	}
}

// ── Handle dispatch for new methods ──────────────────────────────────────────

func TestHandleGovernanceProposals(t *testing.T) {
	h := newTestHandler()
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "inso_getGovernanceProposals",
		ID:      json.RawMessage(`1`),
	}
	resp := h.Handle(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
}

func TestHandleRestakingStats(t *testing.T) {
	h := newTestHandler()
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "inso_getRestakingStats",
		ID:      json.RawMessage(`1`),
	}
	resp := h.Handle(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
}

func TestHandleCrossChainAttestation(t *testing.T) {
	h := newTestHandler()
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "inso_getCrossChainAttestation",
		Params:  json.RawMessage(`["0xdeadbeef"]`),
		ID:      json.RawMessage(`1`),
	}
	resp := h.Handle(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
}

func TestHandleUnknownMethod(t *testing.T) {
	h := newTestHandler()
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "nonexistent_method",
		ID:      json.RawMessage(`1`),
	}
	resp := h.Handle(context.Background(), req)
	if resp.Error == nil {
		t.Error("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected error code -32601, got %d", resp.Error.Code)
	}
}

func TestHandleEthChainId(t *testing.T) {
	h := newTestHandler()
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "eth_chainId",
		ID:      json.RawMessage(`1`),
	}
	resp := h.Handle(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
	if resp.Result == nil {
		t.Fatal("expected result")
	}
}

func TestHandleEthGasPrice(t *testing.T) {
	h := newTestHandler()
	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "eth_gasPrice",
		ID:      json.RawMessage(`1`),
	}
	resp := h.Handle(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}
}

// ── parseBlockParam tests ────────────────────────────────────────────────────

func TestParseBlockParamNumeric(t *testing.T) {
	h := newTestHandler()
	n, err := h.parseBlockParam(json.RawMessage(`[42]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 42 {
		t.Errorf("expected 42, got %d", n)
	}
}

func TestParseBlockParamHex(t *testing.T) {
	h := newTestHandler()
	n, err := h.parseBlockParam(json.RawMessage(`["0x1a"]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 26 {
		t.Errorf("expected 26, got %d", n)
	}
}

func TestParseBlockParamMissing(t *testing.T) {
	h := newTestHandler()
	_, err := h.parseBlockParam(json.RawMessage(`[]`))
	if err == nil {
		t.Error("expected error for missing param")
	}
}
