package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-sequencer/internal/batcher"
	"github.com/insoblok/inso-sequencer/internal/config"
	"github.com/insoblok/inso-sequencer/internal/execution"
	"github.com/insoblok/inso-sequencer/internal/fees"
	"github.com/insoblok/inso-sequencer/internal/genesis"
	"github.com/insoblok/inso-sequencer/internal/mempool"
	"github.com/insoblok/inso-sequencer/internal/metrics"
	"github.com/insoblok/inso-sequencer/internal/producer"
	"github.com/insoblok/inso-sequencer/internal/rpc"
	"github.com/insoblok/inso-sequencer/internal/state"
	"github.com/insoblok/inso-sequencer/internal/tastescore"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	// Setup structured logging
	handler := log.NewTerminalHandler(os.Stdout, true)
	log.SetDefault(log.NewLogger(handler))

	logger := log.New("module", "main")
	logger.Info("InSo Sequencer starting", "version", version)

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("Failed to load config", "err", err)
		os.Exit(1)
	}

	// Sequencer address (in production, derived from private key)
	sequencerAddr := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")

	// --- Phase 2: Persistent state + EVM ---

	// Load genesis configuration
	genesisPath := filepath.Join(filepath.Dir(*configPath), "genesis.json")
	var gen *genesis.Genesis
	gen, err = genesis.LoadGenesis(genesisPath)
	if err != nil {
		logger.Warn("Genesis file not found, using defaults", "path", genesisPath, "err", err)
		gen = genesis.DefaultGenesis()
	}
	chainConfig := gen.ChainConfig()
	logger.Info("Genesis loaded", "chainID", chainConfig.ChainID, "accounts", len(gen.Alloc))

	// Open persistent state database
	dataDir := cfg.Sequencer.DataDir
	if dataDir == "" {
		dataDir = "./data"
	}
	stateStore, err := execution.NewStateStore(dataDir)
	if err != nil {
		logger.Error("Failed to open state database", "err", err)
		os.Exit(1)
	}
	logger.Info("State database opened", "dataDir", dataDir)

	// Initialize state manager (loads from DB or initializes genesis)
	stateManager, err := state.NewManager(cfg.Sequencer.ChainID, sequencerAddr, stateStore, chainConfig, gen)
	if err != nil {
		logger.Error("Failed to initialize state manager", "err", err)
		os.Exit(1)
	}
	defer stateManager.Close()
	logger.Info("State manager initialized",
		"chainID", cfg.Sequencer.ChainID,
		"currentBlock", stateManager.CurrentBlock(),
		"stateRoot", stateManager.GetLatestStateRoot().Hex()[:10],
	)

	// Initialize TasteScore client
	tsClient := tastescore.New(&cfg.TasteScore)
	logger.Info("TasteScore client initialized",
		"enabled", cfg.TasteScore.Enabled,
		"apiUrl", cfg.TasteScore.APIURL,
	)

	// Initialize laned mempool (reputation-gated execution lanes)
	mp := mempool.NewLanedMempool(cfg.TasteScore.OrderingWeight, cfg.Sequencer.MaxTxPerBlock*10)
	logger.Info("Laned mempool initialized",
		"tasteScoreWeight", cfg.TasteScore.OrderingWeight,
		"maxSize", cfg.Sequencer.MaxTxPerBlock*10,
	)

	// Initialize block producer with adaptive sizing and compute receipts
	feeModel := fees.NewDynamicFeeModel()
	blockProducer := producer.New(&cfg.Sequencer, mp, stateManager, sequencerAddr, feeModel)

	// Attach adaptive block sizer (Feature #4)
	adaptiveSizer := producer.NewAdaptiveBlockSizer(producer.AdaptiveSizerConfig{
		MinGasLimit:       cfg.AdaptiveBlock.MinGasLimit,
		MaxGasLimit:       cfg.AdaptiveBlock.MaxGasLimit,
		InitialGasLimit:   cfg.Sequencer.MaxBlockGas,
		MinMaxTx:          cfg.AdaptiveBlock.MinMaxTx,
		MaxMaxTx:          cfg.AdaptiveBlock.MaxMaxTx,
		InitialMaxTx:      cfg.Sequencer.MaxTxPerBlock,
		Alpha:             cfg.AdaptiveBlock.Alpha,
		TargetUtilization: cfg.AdaptiveBlock.TargetUtilization,
		AdjustStepBps:     cfg.AdaptiveBlock.AdjustStepBps,
	})
	blockProducer.SetAdaptiveSizer(adaptiveSizer)

	// Attach verifiable compute receipt store (Feature #10)
	receiptStore := execution.NewReceiptStore()
	blockProducer.SetReceiptStore(receiptStore)

	logger.Info("Dynamic fee model initialized",
		"baseFee", feeModel.BaseFee(),
		"sovereigntyDiscounts", cfg.Sovereignty.FeeDiscounts,
	)

	// Initialize batch submitter with simulated L1 client (devnet)
	l1Client := batcher.NewSimulatedL1Client()
	batchSubmitter := batcher.New(&cfg.L1, stateManager, l1Client, cfg.Sequencer.ChainID)

	// Initialize RPC handler & server
	rpcHandler := rpc.NewHandler(mp, stateManager, tsClient, cfg.Sequencer.ChainID, feeModel)
	rpcHandler.SetReceiptStore(receiptStore)
	rpcHandler.SetLanedPool(mp)
	rpcHandler.SetAdaptiveSizer(adaptiveSizer)
	rpcServer := rpc.NewServer(&cfg.Sequencer, rpcHandler)

	// Start all services
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start RPC server
	if err := rpcServer.Start(ctx); err != nil {
		logger.Error("Failed to start RPC server", "err", err)
		os.Exit(1)
	}
	logger.Info("RPC server started",
		"http", cfg.Sequencer.ListenAddr,
		"ws", cfg.Sequencer.WSAddr,
	)

	// Start block producer
	go blockProducer.Start(ctx)
	logger.Info("Block producer started", "blockTime", cfg.Sequencer.BlockTime)

	// Start batch submitter
	go batchSubmitter.Start(ctx)
	logger.Info("Batch submitter started", "interval", cfg.L1.SubmissionInterval)

	// Start Prometheus metrics endpoint
	met := metrics.New()
	metricsAddr := cfg.Metrics.Addr
	if metricsAddr == "" {
		metricsAddr = "0.0.0.0:6060"
	}
	met.Serve(metricsAddr)
	logger.Info("Metrics server started", "addr", metricsAddr)

	// Wire metrics into components
	blockProducer.SetMetrics(met)
	rpcHandler.SetMetrics(met)
	batchSubmitter.SetMetrics(met)

	fmt.Println()
	logger.Info("═══════════════════════════════════════════════")
	logger.Info("  InSo Sequencer is running")
	logger.Info("  JSON-RPC: " + cfg.Sequencer.ListenAddr)
	logger.Info("  WebSocket: " + cfg.Sequencer.WSAddr)
	logger.Info("  Chain ID: " + fmt.Sprintf("%d", cfg.Sequencer.ChainID))
	logger.Info("═══════════════════════════════════════════════")
	fmt.Println()

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("Received shutdown signal", "signal", sig)

	// Graceful shutdown
	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	blockProducer.Stop()
	batchSubmitter.Stop()
	if err := rpcServer.Stop(shutdownCtx); err != nil {
		logger.Error("Error during shutdown", "err", err)
	}

	logger.Info("InSo Sequencer stopped gracefully")
}
