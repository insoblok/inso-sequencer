package execution

import (
	"fmt"
	"math"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/misc/eip4844"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

// EVMExecutor wraps go-ethereum's EVM to execute transactions against a state.
type EVMExecutor struct {
	chainConfig *params.ChainConfig
	vmConfig    vm.Config
	logger      log.Logger
}

// NewEVMExecutor creates a new EVM execution engine for the given chain config.
func NewEVMExecutor(chainConfig *params.ChainConfig) *EVMExecutor {
	return &EVMExecutor{
		chainConfig: chainConfig,
		vmConfig:    vm.Config{},
		logger:      log.New("module", "evm"),
	}
}

// ExecutionContext holds the block context for a batch of tx executions.
type ExecutionContext struct {
	BlockNumber *big.Int
	Timestamp   uint64
	Coinbase    common.Address // sequencer fee recipient
	GasLimit    uint64
	BaseFee     *big.Int
	ParentHash  common.Hash
}

// ExecutionResult holds the result of executing a single transaction.
type ExecutionResult struct {
	Receipt     *types.Receipt
	GasUsed     uint64
	Err         error          // consensus error (tx invalid — should be dropped)
	VMErr       error          // EVM execution error (tx failed but is included)
}

// ExecuteTransactions runs a set of transactions against the given stateDB
// and returns receipts. Transactions that fail validation are skipped and
// returned in the `skipped` slice.
func (e *EVMExecutor) ExecuteTransactions(
	stateDB *state.StateDB,
	txs []*types.Transaction,
	ctx *ExecutionContext,
	signer types.Signer,
) (executed []*ExecutionResult, skipped []*types.Transaction, cumulativeGas uint64) {

	// Build block context
	blockCtx := vm.BlockContext{
		CanTransfer: core.CanTransfer,
		Transfer:    core.Transfer,
		GetHash:     e.getHashFunc(ctx.ParentHash, ctx.BlockNumber.Uint64()),
		Coinbase:    ctx.Coinbase,
		BlockNumber: new(big.Int).Set(ctx.BlockNumber),
		Time:        ctx.Timestamp,
		GasLimit:    ctx.GasLimit,
		BaseFee:     new(big.Int).Set(ctx.BaseFee),
		BlobBaseFee: eip4844.CalcBlobFee(1), // minimal blob base fee
		Random:      &common.Hash{},         // PoS: no real random in sequencer
	}

	for i, tx := range txs {
		// Skip if block gas limit would be exceeded
		if cumulativeGas+tx.Gas() > ctx.GasLimit {
			skipped = append(skipped, tx)
			continue
		}

		snap := stateDB.Snapshot()

		result := e.applyTransaction(stateDB, tx, &blockCtx, signer, cumulativeGas, i)
		if result.Err != nil {
			// Consensus-level error: tx is invalid, revert and skip
			stateDB.RevertToSnapshot(snap)
			skipped = append(skipped, tx)
			e.logger.Debug("Transaction skipped (invalid)",
				"hash", tx.Hash().Hex(),
				"err", result.Err,
			)
			continue
		}

		cumulativeGas += result.GasUsed
		result.Receipt.CumulativeGasUsed = cumulativeGas
		executed = append(executed, result)
	}

	return executed, skipped, cumulativeGas
}

// applyTransaction executes a single transaction against the EVM.
func (e *EVMExecutor) applyTransaction(
	stateDB *state.StateDB,
	tx *types.Transaction,
	blockCtx *vm.BlockContext,
	signer types.Signer,
	cumulativeGas uint64,
	txIndex int,
) *ExecutionResult {

	// Derive sender
	sender, err := types.Sender(signer, tx)
	if err != nil {
		return &ExecutionResult{Err: fmt.Errorf("invalid sender: %w", err)}
	}

	// Nonce check
	stateNonce := stateDB.GetNonce(sender)
	if tx.Nonce() != stateNonce {
		return &ExecutionResult{Err: fmt.Errorf("nonce mismatch: tx=%d state=%d", tx.Nonce(), stateNonce)}
	}

	// Balance check (value + gas cost)
	gasCost := new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas()), tx.GasPrice())
	totalCost := new(big.Int).Add(gasCost, tx.Value())
	if stateDB.GetBalance(sender).ToBig().Cmp(totalCost) < 0 {
		return &ExecutionResult{Err: fmt.Errorf("insufficient balance: have=%s need=%s",
			stateDB.GetBalance(sender).ToBig().String(), totalCost.String())}
	}

	// Create EVM instance
	txCtx := vm.TxContext{
		Origin:   sender,
		GasPrice: tx.GasPrice(),
	}
	evm := vm.NewEVM(*blockCtx, txCtx, stateDB, e.chainConfig, e.vmConfig)

	// Prepare state for this transaction
	stateDB.SetTxContext(tx.Hash(), txIndex)

	// Deduct gas upfront
	gasCostU256, _ := uint256.FromBig(gasCost)
	stateDB.SubBalance(sender, gasCostU256, tracing.BalanceDecreaseGasBuy)

	// For message calls, we increment nonce here.
	// For contract creation, evm.Create() bumps nonce internally.
	if tx.To() != nil {
		stateDB.SetNonce(sender, stateNonce+1)
	}

	var (
		gasUsed   uint64
		vmErr     error
		result    []byte
		leftover  uint64
	)

	gas := tx.Gas()
	intrinsicGas, err := core.IntrinsicGas(tx.Data(), tx.AccessList(), tx.To() == nil, true, true, true)
	if err != nil {
		return &ExecutionResult{Err: fmt.Errorf("intrinsic gas calculation failed: %w", err)}
	}
	if gas < intrinsicGas {
		return &ExecutionResult{Err: fmt.Errorf("intrinsic gas too low: have=%d need=%d", gas, intrinsicGas)}
	}

	gas -= intrinsicGas

	valueU256, _ := uint256.FromBig(tx.Value())
	if tx.To() == nil {
		// Contract creation
		result, _, leftover, vmErr = evm.Create(vm.AccountRef(sender), tx.Data(), gas, valueU256)
		_ = result
	} else {
		// Message call
		result, leftover, vmErr = evm.Call(vm.AccountRef(sender), *tx.To(), tx.Data(), gas, valueU256)
		_ = result
	}

	gasUsed = tx.Gas() - leftover - intrinsicGas + intrinsicGas // = tx.Gas() - leftover
	gasUsed = tx.Gas() - leftover

	// Refund unused gas
	refund := stateDB.GetRefund()
	maxRefund := gasUsed / 2
	if refund > maxRefund {
		refund = maxRefund
	}
	gasUsed -= refund
	refundAmount := new(big.Int).Mul(new(big.Int).SetUint64(leftover+refund), tx.GasPrice())
	refundU256, _ := uint256.FromBig(refundAmount)
	stateDB.AddBalance(sender, refundU256, tracing.BalanceIncreaseGasReturn)

	// Pay coinbase
	minerFee := new(big.Int).Mul(new(big.Int).SetUint64(gasUsed), tx.GasPrice())
	minerFeeU256, _ := uint256.FromBig(minerFee)
	stateDB.AddBalance(blockCtx.Coinbase, minerFeeU256, tracing.BalanceIncreaseRewardTransactionFee)

	// Build receipt
	receipt := &types.Receipt{
		Type:              tx.Type(),
		TxHash:            tx.Hash(),
		GasUsed:           gasUsed,
		CumulativeGasUsed: cumulativeGas + gasUsed,
		BlockNumber:       blockCtx.BlockNumber,
		TransactionIndex:  uint(txIndex),
	}

	if vmErr != nil {
		receipt.Status = types.ReceiptStatusFailed
	} else {
		receipt.Status = types.ReceiptStatusSuccessful
	}

	// Contract address for creation txs
	if tx.To() == nil {
		receipt.ContractAddress = crypto.CreateAddress(sender, tx.Nonce())
	}

	// Collect logs (must be non-nil for JSON roundtrip)
	receipt.Logs = stateDB.GetLogs(tx.Hash(), blockCtx.BlockNumber.Uint64(), common.Hash{})
	if receipt.Logs == nil {
		receipt.Logs = []*types.Log{}
	}

	// Create bloom filter from logs
	receipt.Bloom = types.CreateBloom(types.Receipts{receipt})

	return &ExecutionResult{
		Receipt: receipt,
		GasUsed: gasUsed,
		VMErr:   vmErr,
	}
}

// CallContract executes a read-only call (eth_call) without modifying state.
func (e *EVMExecutor) CallContract(
	stateDB *state.StateDB,
	msg *core.Message,
	blockCtx *vm.BlockContext,
) ([]byte, uint64, error) {

	txCtx := vm.TxContext{
		Origin:   msg.From,
		GasPrice: msg.GasPrice,
	}
	evm := vm.NewEVM(*blockCtx, txCtx, stateDB, e.chainConfig, e.vmConfig)

	gas := msg.GasLimit
	if gas == 0 {
		gas = math.MaxUint64 / 2
	}

	var (
		result  []byte
		leftover uint64
		err     error
	)

	valueU256, _ := uint256.FromBig(msg.Value)
	if valueU256 == nil {
		valueU256 = new(uint256.Int)
	}
	if msg.To == nil {
		result, _, leftover, err = evm.Create(vm.AccountRef(msg.From), msg.Data, gas, valueU256)
	} else {
		result, leftover, err = evm.Call(vm.AccountRef(msg.From), *msg.To, msg.Data, gas, valueU256)
	}

	return result, gas - leftover, err
}

// EstimateGas runs a binary search to find the minimum gas for a transaction.
func (e *EVMExecutor) EstimateGas(
	stateDB *state.StateDB,
	msg *core.Message,
	blockCtx *vm.BlockContext,
) (uint64, error) {

	// Binary search between intrinsic gas and block gas limit
	lo := uint64(21000)
	hi := blockCtx.GasLimit

	if msg.To == nil {
		// Contract creation has higher intrinsic cost
		lo = 53000
	}

	// First check: does the tx succeed at the upper bound?
	msgCheck := *msg
	msgCheck.GasLimit = hi
	snap := stateDB.Snapshot()
	_, gasUsedAtMax, errMax := e.CallContract(stateDB, &msgCheck, blockCtx)
	stateDB.RevertToSnapshot(snap)

	if errMax != nil {
		// If it fails even with max gas, return a reasonable default
		// with 20% headroom above the gas already used, but not more than block limit
		e.logger.Warn("EstimateGas: call failed even at max gas",
			"maxGas", hi,
			"error", errMax,
		)
		// If the call used some gas before failing, estimate from that
		if gasUsedAtMax > 0 {
			estimate := gasUsedAtMax * 120 / 100
			if estimate > hi {
				estimate = hi
			}
			return estimate, nil
		}
		// Default: return a moderate estimate for contract creation
		if msg.To == nil {
			return 3_000_000, nil
		}
		return 100_000, nil
	}

	for lo+1 < hi {
		mid := (lo + hi) / 2
		msgCopy := *msg
		msgCopy.GasLimit = mid

		snap := stateDB.Snapshot()
		_, _, err := e.CallContract(stateDB, &msgCopy, blockCtx)
		stateDB.RevertToSnapshot(snap)

		if err != nil {
			lo = mid
		} else {
			hi = mid
		}
	}

	return hi, nil
}

// getHashFunc returns a function that retrieves block hashes by number.
// For now it returns empty hashes for historical blocks; this will be
// connected to the chain database in a future iteration.
func (e *EVMExecutor) getHashFunc(parentHash common.Hash, blockNum uint64) func(n uint64) common.Hash {
	return func(n uint64) common.Hash {
		if n == blockNum-1 {
			return parentHash
		}
		return common.Hash{}
	}
}
