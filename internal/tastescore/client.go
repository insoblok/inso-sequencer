package tastescore

import (
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
	score     float64
	fetchedAt time.Time
}

// scoreResponse is the API response structure.
type scoreResponse struct {
	Address    string  `json:"address"`
	TasteScore float64 `json:"tasteScore"`
	Tier       string  `json:"tier"`
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
// Returns a cached result if available and not expired.
func (c *Client) GetScore(ctx context.Context, addr common.Address) (float64, error) {
	if !c.cfg.Enabled {
		return 0.5, nil // default neutral score when disabled
	}

	// Check cache
	c.mu.RLock()
	if cached, ok := c.cache[addr]; ok {
		if time.Since(cached.fetchedAt) < c.cfg.CacheTTL {
			c.mu.RUnlock()
			return cached.score, nil
		}
	}
	c.mu.RUnlock()

	// Fetch from API
	score, err := c.fetchScore(ctx, addr)
	if err != nil {
		c.logger.Warn("TasteScore API error, using default", "addr", addr.Hex(), "err", err)
		return 0.5, nil // degrade gracefully
	}

	// Update cache
	c.mu.Lock()
	c.cache[addr] = cachedScore{score: score, fetchedAt: time.Now()}
	c.mu.Unlock()

	return score, nil
}

// fetchScore makes the actual API call.
func (c *Client) fetchScore(ctx context.Context, addr common.Address) (float64, error) {
	url := fmt.Sprintf("%s/v1/score/%s", c.cfg.APIURL, addr.Hex())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("X-API-Key", c.cfg.APIKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("api call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("api returned status %d", resp.StatusCode)
	}

	var result scoreResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("decode response: %w", err)
	}

	return result.TasteScore, nil
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
