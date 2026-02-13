package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/log"

	"github.com/insoblok/inso-sequencer/internal/batcher"
	"github.com/insoblok/inso-sequencer/internal/config"
	"github.com/insoblok/inso-sequencer/internal/mempool"
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

	// Initialize state manager
	stateManager := state.NewManager(cfg.Sequencer.ChainID, sequencerAddr)
	logger.Info("State manager initialized", "chainID", cfg.Sequencer.ChainID)

	// Initialize TasteScore client
	tsClient := tastescore.New(&cfg.TasteScore)
	logger.Info("TasteScore client initialized",
		"enabled", cfg.TasteScore.Enabled,
		"apiUrl", cfg.TasteScore.APIURL,
	)

	// Initialize mempool
	mp := mempool.New(cfg.TasteScore.OrderingWeight, cfg.Sequencer.MaxTxPerBlock*10)
	logger.Info("Mempool initialized",
		"tasteScoreWeight", cfg.TasteScore.OrderingWeight,
		"maxSize", cfg.Sequencer.MaxTxPerBlock*10,
	)

	// Initialize block producer
	blockProducer := producer.New(&cfg.Sequencer, mp, stateManager, sequencerAddr)

	// Initialize batch submitter
	batchSubmitter := batcher.New(&cfg.L1, stateManager)

	// Initialize RPC handler & server
	rpcHandler := rpc.NewHandler(mp, stateManager, tsClient, cfg.Sequencer.ChainID)
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
