package mempool

import "errors"

var (
	// ErrAlreadyKnown is returned when adding a transaction that already exists in the pool.
	ErrAlreadyKnown = errors.New("transaction already known")

	// ErrMempoolFull is returned when the mempool has reached its maximum capacity.
	ErrMempoolFull = errors.New("mempool is full")

	// ErrInvalidSender is returned when the transaction sender cannot be derived.
	ErrInvalidSender = errors.New("invalid sender")

	// ErrNonceTooLow is returned when the transaction nonce is below the sender's current nonce.
	ErrNonceTooLow = errors.New("nonce too low")

	// ErrGasTooLow is returned when the gas price is below the minimum accepted.
	ErrGasTooLow = errors.New("gas price too low")
)
