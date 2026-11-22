package analytics

import (
	"log/slog"
	"sync"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
)

// Store runs the read-only reporting queries behind the AI Cost Analyser
// screens. It reads the same metastore tables the conversation DAO in
// agents/core writes.
type Store struct {
	dbManager *common.DatabaseManager
}

var (
	store      *Store
	storeMutex sync.Mutex
)

// GetStore returns the shared Store, connecting to the metastore on first use.
// It returns nil when the database manager cannot be created.
func GetStore() *Store {
	storeMutex.Lock()
	defer storeMutex.Unlock()

	if store != nil {
		return store
	}

	dbManager, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		slog.Error("analytics: unable to get database manager", "error", err)
		return nil
	}

	store = &Store{dbManager: dbManager}
	return store
}

func getLogger(context *security.RequestContext) *slog.Logger {
	if context != nil && context.GetLogger() != nil {
		return context.GetLogger()
	}
	return slog.Default()
}
