# InSo Sequencer

The transaction sequencer for the InSoBlok L2 blockchain. Responsible for ordering transactions, producing blocks, and submitting batches to L1.

## Architecture

```
┌──────────────────────────────────────────────────────────┐
│                      InSo Sequencer                       │
├───────────────┬─────────────────┬────────────────────────┤
│  RPC Server   │  Laned Mempool  │  Batch Submitter        │
│  (JSON-RPC)   │  Fast/Std/Slow  │  (L1 Calldata/Blobs)   │
├───────────────┴─────────────────┴────────────────────────┤
│               Block Producer Engine                       │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────────┐  │
│  │  TasteScore  │  │  Adaptive    │  │   Compute      │  │
│  │  Ordering    │  │  Block Sizer │  │   Receipts     │  │
│  └──────────────┘  └──────────────┘  └────────────────┘  │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────────┐  │
│  │  Transaction │  │  State Root  │  │   Receipt      │  │
│  │  Execution   │  │  Computation │  │   Store        │  │
│  └──────────────┘  └──────────────┘  └────────────────┘  │
├──────────────────────────────────────────────────────────┤
│                       EVM Engine                          │
│                  (go-ethereum fork)                        │
└──────────────────────────────────────────────────────────┘
```

## Features

- **Fair Transaction Ordering** — TasteScore-weighted FIFO prevents MEV and front-running
- **Reputation-Gated Execution Lanes** — Fast/Standard/Slow lanes based on sovereignty tiers
- **Adaptive Block Architecture** — EMA-based gas limits (15M–60M) and tx caps (1K–10K) that respond to utilization
- **Verifiable Compute Receipts** — Per-transaction receipts with Merkle root for verifiable execution proofs
- **High Throughput** — Targets 10,000+ TPS with sub-second block times
- **Fixed Fees** — Predictable transaction costs independent of network congestion
- **EVM Compatible** — Full EVM equivalence via go-ethereum execution engine
- **Batch Submission** — Efficient L1 data posting via calldata or EIP-4844 blobs

## Prerequisites

- Go 1.22+
- Docker (optional, for containerized runs)
- Make

## Quick Start

### Build from source
```bash
make build
```

### Run locally
```bash
make run
```

### Run with Docker
```bash
docker build -t inso-sequencer .
docker run -p 8545:8545 -p 8546:8546 inso-sequencer
```

### Run tests
```bash
make test
```

## Configuration

Configuration is via environment variables or a YAML config file:

```yaml
# config.yaml
sequencer:
  listen_addr: "0.0.0.0:8545"
  ws_addr: "0.0.0.0:8546"
  block_time: 500ms
  max_block_gas: 30000000

l1:
  rpc_url: "https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY"
  chain_id: 1
  batch_submitter_key: "${BATCH_SUBMITTER_PRIVATE_KEY}"

tastescore:
  api_url: "https://api.insoblokai.io"
  api_key: "${TASTESCORE_API_KEY}"
  ordering_weight: 0.3
```

| Variable | Default | Description |
|----------|---------|-------------|
| `INSO_LISTEN_ADDR` | `0.0.0.0:8545` | JSON-RPC listen address |
| `INSO_WS_ADDR` | `0.0.0.0:8546` | WebSocket listen address |
| `INSO_BLOCK_TIME` | `500ms` | Target block production interval |
| `INSO_L1_RPC_URL` | — | L1 Ethereum RPC endpoint |
| `INSO_BATCH_SUBMITTER_KEY` | — | Private key for L1 batch submission |
| `INSO_TASTESCORE_API_KEY` | — | TasteScore API key for fair ordering |

## Project Structure

```
inso-sequencer/
├── cmd/
│   └── sequencer/          # Main entry point
│       └── main.go
├── internal/
│   ├── config/             # Configuration loading
│   ├── rpc/                # JSON-RPC & WebSocket server
│   ├── mempool/            # Transaction pool with fair ordering
│   │   ├── mempool.go      #   Basic FIFO mempool
│   │   ├── pool.go         #   TxPool interface (shared by Mempool & LanedMempool)
│   │   └── lanes.go        #   Reputation-gated execution lanes (Fast/Std/Slow)
│   ├── producer/           # Block production engine
│   │   ├── producer.go     #   Block producer (adaptive gas limits + receipt generation)
│   │   └── adaptive.go     #   Adaptive block sizer (EMA-based utilization tracking)
│   ├── execution/          # EVM transaction execution
│   │   ├── evm.go          #   EVM executor
│   │   ├── statestore.go   #   State store (PebbleDB)
│   │   ├── chaindb.go      #   Chain database
│   │   └── receipts.go     #   Verifiable compute receipts + Merkle root
│   ├── batcher/            # L1 batch submission
│   ├── fees/               # Fee management
│   ├── genesis/            # Genesis state initialization
│   ├── metrics/            # Prometheus metrics
│   ├── tastescore/         # TasteScore integration client
│   └── state/              # State management & root computation
├── pkg/
│   └── types/              # Shared types (Block, Transaction, etc.)
├── Makefile
├── Dockerfile
├── go.mod
└── README.md
```

## JSON-RPC Methods

Standard Ethereum JSON-RPC plus InSoBlok extensions:

| Method | Description |
|--------|-------------|
| `eth_sendRawTransaction` | Submit a signed transaction |
| `eth_getBlockByNumber` | Get block by number |
| `eth_getTransactionReceipt` | Get transaction receipt |
| `inso_getSequencerStatus` | Sequencer health & sync status |
| `inso_getTasteScoreOrdering` | Current TasteScore ordering queue |
| `inso_getBatchStatus` | L1 batch submission status |
| `inso_getLaneStats` | Execution lane stats (count, gas per lane + thresholds) |
| `inso_getAdaptiveBlockStats` | Current adaptive block parameters (gas limit, max tx, utilization EMA) |
| `inso_getComputeReceipt` | Retrieve verifiable compute receipt by transaction hash |
| `inso_getBlockReceiptRoot` | Get Merkle receipt root for all transactions in a block |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Apache-2.0
