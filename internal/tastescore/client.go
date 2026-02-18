package tastescore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-sequencer/internal/config"
)

// Client is the TasteScore API client with caching.
type Client struct {
	cfg    *config.TasteScoreConfig
	http   *http.Client
	cache  map[common.Address]cachedScore
	mu     sync.RWMutex
	logger log.Logger
}

type cachedScore struct {
	result    *ScoreResult
	fetchedAt time.Time
}

// ScoreResult holds the full V2 API response fields.
type ScoreResult struct {
	Address        string             `json:"address"`
	CompositeScore float64            `json:"composite_score"`
	Confidence     float64            `json:"confidence"`
	Tier           string             `json:"tier"`
	ModelVersion   string             `json:"model_version"`
	ChainBreakdown map[string]float64 `json:"chain_breakdown"`
}

// scoreRequest is the POST body for the V2 /tastescore/details endpoint.
type scoreRequest struct {
	Address string   `json:"address"`
	Chains  []string `json:"chains,omitempty"`
}

// v2APIResponse wraps the V2 envelope from the backend.
type v2APIResponse struct {
	Status  string          `json:"status"`
	Summary json.RawMessage `json:"summary"`
}

// v2Summary is the summary object inside the V2 envelope.
type v2Summary struct {
	Address       string             `json:"address"`
	ChainsChecked []string           `json:"chains_checked"`
	TrustProfile  v2TrustProfile     `json:"trust_profile"`
	TasteScoreEngine v2TasteScoreEngine `json:"tastescore_engine"`
}

type v2TrustProfile struct {
	Score      float64 `json:"score"`
	Label      string  `json:"label"`
	Confidence float64 `json:"confidence"`
}

type v2TasteScoreEngine struct {
	Score      float64  `json:"score"`
	Label      string   `json:"label"`
	Confidence float64  `json:"confidence"`
	ModelVer   string   `json:"model_ver"`
	Reasons    []string `json:"reasons"`
}

// New creates a new TasteScore client.
func New(cfg *config.TasteScoreConfig) *Client {
	return &Client{
		cfg: cfg,
		http: &http.Client{
			Timeout: 5 * time.Second,
		},
		cache:  make(map[common.Address]cachedScore, 1024),
		logger: log.New("module", "tastescore"),
	}
}

// GetScore retrieves the TasteScore for a wallet address.
// Returns the composite score (0.0–1.0). Uses cached V2 result if available.
func (c *Client) GetScore(ctx context.Context, addr common.Address) (float64, error) {
	result, err := c.GetScoreResult(ctx, addr)
	if err != nil {
		return 0.5, err
	}
	return result.CompositeScore, nil
}

// GetScoreResult retrieves the full V2 ScoreResult for a wallet address.
// Returns a cached result if available and not expired.
func (c *Client) GetScoreResult(ctx context.Context, addr common.Address) (*ScoreResult, error) {
	if !c.cfg.Enabled {
		return &ScoreResult{
			Address:        addr.Hex(),
			CompositeScore: 0.5,
			Confidence:     0.0,
			Tier:           "Unscored",
			ModelVersion:   "disabled",
		}, nil
	}

	// Check cache
	c.mu.RLock()
	if cached, ok := c.cache[addr]; ok {
		if time.Since(cached.fetchedAt) < c.cfg.CacheTTL {
			c.mu.RUnlock()
			return cached.result, nil
		}
	}
	c.mu.RUnlock()

	// Fetch from API
	result, err := c.fetchScore(ctx, addr)
	if err != nil {
		c.logger.Warn("TasteScore API error, using default", "addr", addr.Hex(), "err", err)
		return &ScoreResult{
			Address:        addr.Hex(),
			CompositeScore: 0.5,
			Confidence:     0.0,
			Tier:           "Unknown",
			ModelVersion:   "fallback",
		}, nil
	}

	// Update cache
	c.mu.Lock()
	c.cache[addr] = cachedScore{result: result, fetchedAt: time.Now()}
	c.mu.Unlock()

	return result, nil
}

// EffectiveScore returns the confidence-weighted score used for tx ordering.
// effectiveFee = baseFee × (1 - weight × score × confidence)
func (c *Client) EffectiveScore(result *ScoreResult) float64 {
	if result == nil {
		return 0.5
	}
	// Weight the score by confidence: low-confidence scores get reduced impact.
	return result.CompositeScore * result.Confidence
}

// fetchScore makes the actual V2 API call.
func (c *Client) fetchScore(ctx context.Context, addr common.Address) (*ScoreResult, error) {
	// Build V2 POST request
	reqBody := scoreRequest{
		Address: addr.Hex(),
		Chains:  c.cfg.Chains,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/api/v2/tastescore/details", c.cfg.APIURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("X-API-Key", c.cfg.APIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api returned status %d", resp.StatusCode)
	}

	// Parse V2 envelope
	var envelope v2APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode envelope: %w", err)
	}

	// Parse summary from envelope
	var summary v2Summary
	if err := json.Unmarshal(envelope.Summary, &summary); err != nil {
		return nil, fmt.Errorf("decode summary: %w", err)
	}

	// Build chain breakdown from chains checked
	chainBreakdown := make(map[string]float64, len(summary.ChainsChecked))
	for _, chain := range summary.ChainsChecked {
		chainBreakdown[chain] = summary.TasteScoreEngine.Score
	}

	// Determine tier from score
	tier := classifyTier(summary.TasteScoreEngine.Score)

	return &ScoreResult{
		Address:        summary.Address,
		CompositeScore: summary.TasteScoreEngine.Score,
		Confidence:     summary.TasteScoreEngine.Confidence,
		Tier:           tier,
		ModelVersion:   summary.TasteScoreEngine.ModelVer,
		ChainBreakdown: chainBreakdown,
	}, nil
}

// classifyTier returns the TasteScore tier label from a 0.0–1.0 score.
func classifyTier(score float64) string {
	switch {
	case score >= 0.75:
		return "Platinum"
	case score >= 0.50:
		return "Gold"
	case score >= 0.25:
		return "Silver"
	case score > 0:
		return "Bronze"
	default:
		return "Unscored"
	}
}

// ClearCache removes all cached scores.
func (c *Client) ClearCache() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[common.Address]cachedScore, 1024)
}

// CacheSize returns the number of cached entries.
func (c *Client) CacheSize() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}
