package tastescore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/insoblok/inso-sequencer/internal/config"
)

// mockV2Response returns a realistic V2 API envelope.
func mockV2Response(score, confidence float64, modelVer string) []byte {
	summary := map[string]interface{}{
		"address":        "0x1234567890abcdef1234567890abcdef12345678",
		"chains_checked": []string{"ethereum", "base", "arbitrum", "inso"},
		"trust_profile": map[string]interface{}{
			"score":      score,
			"label":      "Trustworthy",
			"confidence": confidence,
		},
		"tastescore_engine": map[string]interface{}{
			"score":      score,
			"label":      "Good",
			"confidence": confidence,
			"model_ver":  modelVer,
			"reasons":    []string{"Active on-chain history"},
		},
	}

	summaryBytes, _ := json.Marshal(summary)

	envelope := map[string]interface{}{
		"status":  "ok",
		"summary": json.RawMessage(summaryBytes),
	}
	data, _ := json.Marshal(envelope)
	return data
}

func TestFetchScoreV2(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify it's a POST request
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		// Verify Content-Type
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", ct)
		}

		// Verify API key header
		if key := r.Header.Get("X-API-Key"); key != "test-key" {
			t.Errorf("expected API key test-key, got %s", key)
		}

		// Verify request body
		var reqBody scoreRequest
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}
		if len(reqBody.Chains) != 4 {
			t.Errorf("expected 4 chains, got %d", len(reqBody.Chains))
		}

		// Verify endpoint path
		if r.URL.Path != "/api/v2/tastescore/details" {
			t.Errorf("expected path /api/v2/tastescore/details, got %s", r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(mockV2Response(0.72, 0.91, "tastescore-v2.1"))
	}))
	defer server.Close()

	cfg := &config.TasteScoreConfig{
		Enabled:        true,
		APIURL:         server.URL,
		APIKey:         "test-key",
		APIVersion:     "v2",
		Chains:         []string{"ethereum", "base", "arbitrum", "inso"},
		OrderingWeight: 0.3,
		CacheTTL:       60 * time.Second,
	}

	client := New(cfg)
	addr := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	result, err := client.GetScoreResult(context.Background(), addr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.CompositeScore != 0.72 {
		t.Errorf("expected score 0.72, got %f", result.CompositeScore)
	}
	if result.Confidence != 0.91 {
		t.Errorf("expected confidence 0.91, got %f", result.Confidence)
	}
	if result.ModelVersion != "tastescore-v2.1" {
		t.Errorf("expected model tastescore-v2.1, got %s", result.ModelVersion)
	}
	if result.Tier != "Gold" {
		t.Errorf("expected tier Gold, got %s", result.Tier)
	}
	if len(result.ChainBreakdown) != 4 {
		t.Errorf("expected 4 chains in breakdown, got %d", len(result.ChainBreakdown))
	}
}

func TestEffectiveScore(t *testing.T) {
	cfg := &config.TasteScoreConfig{Enabled: true}
	client := New(cfg)

	tests := []struct {
		name       string
		score      float64
		confidence float64
		want       float64
	}{
		{"High confidence", 0.80, 0.95, 0.76},
		{"Low confidence", 0.80, 0.30, 0.24},
		{"Zero confidence", 0.80, 0.0, 0.0},
		{"Perfect score", 1.0, 1.0, 1.0},
		{"Mid score mid confidence", 0.50, 0.50, 0.25},
		{"Nil result", 0, 0, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "Nil result" {
				got := client.EffectiveScore(nil)
				if got != 0.5 {
					t.Errorf("EffectiveScore(nil) = %f, want 0.5", got)
				}
				return
			}
			result := &ScoreResult{
				CompositeScore: tt.score,
				Confidence:     tt.confidence,
			}
			got := client.EffectiveScore(result)
			if diff := got - tt.want; diff > 0.001 || diff < -0.001 {
				t.Errorf("EffectiveScore(%v, %v) = %f, want %f", tt.score, tt.confidence, got, tt.want)
			}
		})
	}
}

func TestCaching(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.Write(mockV2Response(0.65, 0.88, "v2"))
	}))
	defer server.Close()

	cfg := &config.TasteScoreConfig{
		Enabled:    true,
		APIURL:     server.URL,
		APIKey:     "test",
		APIVersion: "v2",
		Chains:     []string{"ethereum"},
		CacheTTL:   1 * time.Minute,
	}

	client := New(cfg)
	addr := common.HexToAddress("0xaaaa")

	// First call — should hit API
	_, err := client.GetScoreResult(context.Background(), addr)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 API call, got %d", callCount)
	}

	// Second call — should be cached
	_, err = client.GetScoreResult(context.Background(), addr)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if callCount != 1 {
		t.Errorf("expected 1 API call (cached), got %d", callCount)
	}

	if client.CacheSize() != 1 {
		t.Errorf("expected cache size 1, got %d", client.CacheSize())
	}

	// Clear cache
	client.ClearCache()
	if client.CacheSize() != 0 {
		t.Errorf("expected cache size 0 after clear, got %d", client.CacheSize())
	}

	// Third call — should hit API again
	_, err = client.GetScoreResult(context.Background(), addr)
	if err != nil {
		t.Fatalf("third call: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected 2 API calls after cache clear, got %d", callCount)
	}
}

func TestDisabled(t *testing.T) {
	cfg := &config.TasteScoreConfig{Enabled: false}
	client := New(cfg)

	result, err := client.GetScoreResult(context.Background(), common.HexToAddress("0xdead"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CompositeScore != 0.5 {
		t.Errorf("disabled score should be 0.5, got %f", result.CompositeScore)
	}
	if result.Confidence != 0.0 {
		t.Errorf("disabled confidence should be 0.0, got %f", result.Confidence)
	}
	if result.Tier != "Unscored" {
		t.Errorf("disabled tier should be Unscored, got %s", result.Tier)
	}
}

func TestAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.TasteScoreConfig{
		Enabled:    true,
		APIURL:     server.URL,
		APIKey:     "test",
		APIVersion: "v2",
		Chains:     []string{"ethereum"},
		CacheTTL:   1 * time.Minute,
	}

	client := New(cfg)
	result, err := client.GetScoreResult(context.Background(), common.HexToAddress("0xbeef"))
	if err != nil {
		t.Fatalf("should degrade gracefully, got error: %v", err)
	}
	// Should return default fallback values
	if result.CompositeScore != 0.5 {
		t.Errorf("fallback score should be 0.5, got %f", result.CompositeScore)
	}
	if result.ModelVersion != "fallback" {
		t.Errorf("fallback model should be 'fallback', got %s", result.ModelVersion)
	}
}

func TestClassifyTier(t *testing.T) {
	tests := []struct {
		score float64
		want  string
	}{
		{0.90, "Platinum"},
		{0.75, "Platinum"},
		{0.74, "Gold"},
		{0.50, "Gold"},
		{0.49, "Silver"},
		{0.25, "Silver"},
		{0.24, "Bronze"},
		{0.01, "Bronze"},
		{0.0, "Unscored"},
	}
	for _, tt := range tests {
		got := classifyTier(tt.score)
		if got != tt.want {
			t.Errorf("classifyTier(%f) = %s, want %s", tt.score, got, tt.want)
		}
	}
}

func TestGetScoreBackwardCompat(t *testing.T) {
	// GetScore should still return a float64 for backward compatibility
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(mockV2Response(0.82, 0.95, "v2"))
	}))
	defer server.Close()

	cfg := &config.TasteScoreConfig{
		Enabled:    true,
		APIURL:     server.URL,
		APIKey:     "test",
		APIVersion: "v2",
		Chains:     []string{"ethereum"},
		CacheTTL:   1 * time.Minute,
	}

	client := New(cfg)
	score, err := client.GetScore(context.Background(), common.HexToAddress("0x1111"))
	if err != nil {
		t.Fatalf("GetScore failed: %v", err)
	}
	if score != 0.82 {
		t.Errorf("GetScore should return composite score 0.82, got %f", score)
	}
}
