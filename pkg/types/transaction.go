package types

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

// TxMeta is the InSoBlok-specific metadata attached to a pending transaction.
type TxMeta struct {
	Tx         *types.Transaction `json:"-"`
	TasteScore float64            `json:"tasteScore"`
	Priority   float64            `json:"priority"`
	ReceivedAt time.Time          `json:"receivedAt"`
	Sender     common.Address     `json:"sender"`
	Nonce      uint64             `json:"nonce"`
	GasPrice   *big.Int           `json:"gasPrice"`
	GasTipCap  *big.Int           `json:"gasTipCap"`
	GasFeeCap  *big.Int           `json:"gasFeeCap"`
}

// ComputePriority calculates the ordering priority using TasteScore weight.
// priority = (1 - weight) * gasPriceFactor + weight * tasteScore
func (m *TxMeta) ComputePriority(tasteScoreWeight float64) {
	gasPriceFactor := 0.5 // normalized; in practice computed relative to base fee
	if m.GasPrice != nil && m.GasPrice.Sign() > 0 {
		// Normalize gas price to 0-1 range (cap at 100 gwei = 1.0)
		maxGwei := new(big.Int).Mul(big.NewInt(100), big.NewInt(1e9))
		gp := new(big.Float).SetInt(m.GasPrice)
		mg := new(big.Float).SetInt(maxGwei)
		ratio, _ := new(big.Float).Quo(gp, mg).Float64()
		if ratio > 1.0 {
			ratio = 1.0
		}
		gasPriceFactor = ratio
	}
	m.Priority = (1-tasteScoreWeight)*gasPriceFactor + tasteScoreWeight*m.TasteScore
}

// SequencerStatus holds the current sequencer state.
type SequencerStatus struct {
	IsSequencing     bool           `json:"isSequencing"`
	CurrentBlock     uint64         `json:"currentBlock"`
	PendingTxCount   int            `json:"pendingTxCount"`
	LastBatchIndex   uint64         `json:"lastBatchIndex"`
	L1Head           uint64         `json:"l1Head"`
	UptimeSeconds    float64        `json:"uptimeSeconds"`
	SequencerAddr    common.Address `json:"sequencerAddr"`
	BlockTime        time.Duration  `json:"blockTime"`
	ChainID          *big.Int       `json:"chainId"`
}
