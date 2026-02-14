package metrics

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/log"
)

// Metrics exposes Prometheus-compatible /metrics endpoint for the sequencer.
type Metrics struct {
	mu sync.RWMutex

	// Block production
	BlockHeight      atomic.Uint64
	BlocksProduced   atomic.Uint64
	LastBlockTime    atomic.Int64 // unix ms
	BlockTimeAvgMs   atomic.Int64
	TxProcessed      atomic.Uint64

	// Mempool
	MempoolSize      atomic.Int64
	MempoolRejected  atomic.Uint64

	// Gas / Fees
	BaseFeeGwei      atomic.Int64 // stored as gwei * 1000 (milli-gwei precision)
	GasUsedTotal     atomic.Uint64
	GasLimitTotal    atomic.Uint64

	// Batching
	BatchesSubmitted atomic.Uint64
	LastBatchSize    atomic.Int64

	// RPC
	RPCRequests      atomic.Uint64
	RPCErrors        atomic.Uint64

	logger log.Logger
}

// New creates a new Metrics instance.
func New() *Metrics {
	return &Metrics{
		logger: log.New("module", "metrics"),
	}
}

// Serve starts the Prometheus metrics HTTP endpoint.
func (m *Metrics) Serve(addr string) {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", m.handleMetrics)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","service":"inso-sequencer","timestamp":%d}`, time.Now().Unix())
	})

	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	go func() {
		m.logger.Info("Metrics server starting", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			m.logger.Error("Metrics server error", "err", err)
		}
	}()
}

func (m *Metrics) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	// Block production metrics
	fmt.Fprintf(w, "# HELP inso_sequencer_block_height Current block height\n")
	fmt.Fprintf(w, "# TYPE inso_sequencer_block_height gauge\n")
	fmt.Fprintf(w, "inso_sequencer_block_height %d\n\n", m.BlockHeight.Load())

	fmt.Fprintf(w, "# HELP inso_sequencer_blocks_produced_total Total blocks produced\n")
	fmt.Fprintf(w, "# TYPE inso_sequencer_blocks_produced_total counter\n")
	fmt.Fprintf(w, "inso_sequencer_blocks_produced_total %d\n\n", m.BlocksProduced.Load())

	fmt.Fprintf(w, "# HELP inso_sequencer_block_time_avg_ms Average block time in milliseconds\n")
	fmt.Fprintf(w, "# TYPE inso_sequencer_block_time_avg_ms gauge\n")
	fmt.Fprintf(w, "inso_sequencer_block_time_avg_ms %d\n\n", m.BlockTimeAvgMs.Load())

	fmt.Fprintf(w, "# HELP inso_sequencer_tx_processed_total Total transactions processed\n")
	fmt.Fprintf(w, "# TYPE inso_sequencer_tx_processed_total counter\n")
	fmt.Fprintf(w, "inso_sequencer_tx_processed_total %d\n\n", m.TxProcessed.Load())

	// Mempool metrics
	fmt.Fprintf(w, "# HELP inso_sequencer_mempool_size Current mempool depth\n")
	fmt.Fprintf(w, "# TYPE inso_sequencer_mempool_size gauge\n")
	fmt.Fprintf(w, "inso_sequencer_mempool_size %d\n\n", m.MempoolSize.Load())

	fmt.Fprintf(w, "# HELP inso_sequencer_mempool_rejected_total Transactions rejected from mempool\n")
	fmt.Fprintf(w, "# TYPE inso_sequencer_mempool_rejected_total counter\n")
	fmt.Fprintf(w, "inso_sequencer_mempool_rejected_total %d\n\n", m.MempoolRejected.Load())

	// Fee model metrics
	fmt.Fprintf(w, "# HELP inso_sequencer_base_fee_mgwei Base fee in milli-gwei (gwei * 1000)\n")
	fmt.Fprintf(w, "# TYPE inso_sequencer_base_fee_mgwei gauge\n")
	fmt.Fprintf(w, "inso_sequencer_base_fee_mgwei %d\n\n", m.BaseFeeGwei.Load())

	fmt.Fprintf(w, "# HELP inso_sequencer_gas_used_total Cumulative gas used\n")
	fmt.Fprintf(w, "# TYPE inso_sequencer_gas_used_total counter\n")
	fmt.Fprintf(w, "inso_sequencer_gas_used_total %d\n\n", m.GasUsedTotal.Load())

	fmt.Fprintf(w, "# HELP inso_sequencer_gas_limit_total Cumulative gas limit\n")
	fmt.Fprintf(w, "# TYPE inso_sequencer_gas_limit_total counter\n")
	fmt.Fprintf(w, "inso_sequencer_gas_limit_total %d\n\n", m.GasLimitTotal.Load())

	// Batch metrics
	fmt.Fprintf(w, "# HELP inso_sequencer_batches_submitted_total Total L1 batches submitted\n")
	fmt.Fprintf(w, "# TYPE inso_sequencer_batches_submitted_total counter\n")
	fmt.Fprintf(w, "inso_sequencer_batches_submitted_total %d\n\n", m.BatchesSubmitted.Load())

	fmt.Fprintf(w, "# HELP inso_sequencer_last_batch_size_bytes Last batch size in bytes\n")
	fmt.Fprintf(w, "# TYPE inso_sequencer_last_batch_size_bytes gauge\n")
	fmt.Fprintf(w, "inso_sequencer_last_batch_size_bytes %d\n\n", m.LastBatchSize.Load())

	// RPC metrics
	fmt.Fprintf(w, "# HELP inso_sequencer_rpc_requests_total Total RPC requests\n")
	fmt.Fprintf(w, "# TYPE inso_sequencer_rpc_requests_total counter\n")
	fmt.Fprintf(w, "inso_sequencer_rpc_requests_total %d\n\n", m.RPCRequests.Load())

	fmt.Fprintf(w, "# HELP inso_sequencer_rpc_errors_total Total RPC errors\n")
	fmt.Fprintf(w, "# TYPE inso_sequencer_rpc_errors_total counter\n")
	fmt.Fprintf(w, "inso_sequencer_rpc_errors_total %d\n\n", m.RPCErrors.Load())
}
