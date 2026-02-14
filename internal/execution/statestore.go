package execution

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/ethereum/go-ethereum/triedb/hashdb"
)

// StateStore manages the persistent state database backed by Pebble
// and provides StateDB instances for EVM execution.
type StateStore struct {
	disk    ethdb.Database  // low-level key-value store (Pebble)
	trieDB  *triedb.Database // trie database (caching + commit)
	logger  log.Logger
}

// NewStateStore opens or creates a persistent state database at the given path.
// If path is empty, an in-memory database is used (for testing).
func NewStateStore(dataDir string) (*StateStore, error) {
	logger := log.New("module", "statestore")

	var disk ethdb.Database
	var err error

	if dataDir == "" {
		// In-memory for tests
		disk = rawdb.NewMemoryDatabase()
		logger.Info("Using in-memory state database")
	} else {
		// Persistent Pebble database
		disk, err = rawdb.NewPebbleDBDatabase(dataDir+"/chaindata", 256, 256, "", false, false)
		if err != nil {
			return nil, fmt.Errorf("open pebble db: %w", err)
		}
		logger.Info("State database opened", "path", dataDir+"/chaindata")
	}

	// Create trie database with hash-based scheme (proven, stable)
	tdb := triedb.NewDatabase(disk, &triedb.Config{
		HashDB: &hashdb.Config{
			CleanCacheSize: 256 * 1024 * 1024, // 256 MB trie cache
		},
	})

	return &StateStore{
		disk:   disk,
		trieDB: tdb,
		logger: logger,
	}, nil
}

// OpenState returns a new StateDB rooted at the given state root.
// Use common.Hash{} for an empty (genesis) state.
func (s *StateStore) OpenState(root common.Hash) (*state.StateDB, error) {
	sdb, err := state.New(root, state.NewDatabaseWithNodeDB(s.disk, s.trieDB), nil)
	if err != nil {
		return nil, fmt.Errorf("open state at root %s: %w", root.Hex(), err)
	}
	return sdb, nil
}

// CommitState finalizes the state changes and writes the trie to disk.
// Returns the new state root hash.
func (s *StateStore) CommitState(sdb *state.StateDB, blockNum uint64) (common.Hash, error) {
	// Finalize the state (compute intermediate root)
	root, err := sdb.Commit(blockNum, true)
	if err != nil {
		return common.Hash{}, fmt.Errorf("commit state: %w", err)
	}

	// Flush trie nodes to disk
	if err := s.trieDB.Commit(root, false); err != nil {
		return common.Hash{}, fmt.Errorf("commit trie: %w", err)
	}

	s.logger.Debug("State committed", "root", root.Hex(), "block", blockNum)
	return root, nil
}

// HasState checks if a state root exists in the database.
func (s *StateStore) HasState(root common.Hash) bool {
	return s.trieDB.Initialized(root)
}

// DiskDB returns the underlying key-value database for direct access
// (e.g., storing block headers, receipts, etc.).
func (s *StateStore) DiskDB() ethdb.Database {
	return s.disk
}

// Close gracefully closes the state store.
func (s *StateStore) Close() error {
	if err := s.trieDB.Close(); err != nil {
		return err
	}
	return s.disk.Close()
}
