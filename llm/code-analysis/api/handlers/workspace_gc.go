package handlers

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"nudgebee/code-analysis-agent/internal/workspacegc"
)

// defaultWorkspaceStorageLimitBytes mirrors llm-server's 5Gi emptyDir sizeLimit
// default and is used when WORKSPACE_STORAGE_LIMIT_BYTES is absent (e.g. a
// workspace pod created before that env was injected).
const defaultWorkspaceStorageLimitBytes = 5 << 30

// workspaceStorageLimitBytes is the pod's ephemeral-storage budget, injected by
// llm-server from the same value it uses for the emptyDir sizeLimit.
func workspaceStorageLimitBytes() int64 {
	if v := os.Getenv("WORKSPACE_STORAGE_LIMIT_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return defaultWorkspaceStorageLimitBytes
}

var workspaceGCs sync.Map // workspace root -> *workspacegc.Manager

// workspaceGCFor returns the process-wide coordinator for a persistent
// workspace. Cache paths are deliberately explicit and language-specific
// only at the configuration boundary; the manager itself just removes
// whitelisted generated directories.
func workspaceGCFor(workspaceRoot string) *workspacegc.Manager {
	if workspaceRoot == "" {
		workspaceRoot = "/tmp/code-analysis"
	}
	workspaceRoot = filepath.Clean(workspaceRoot)
	if existing, ok := workspaceGCs.Load(workspaceRoot); ok {
		return existing.(*workspacegc.Manager)
	}

	cacheRoots := workspacegc.CachePaths(workspaceRoot)
	manager := workspacegc.New(workspaceRoot, cacheRoots, workspaceStorageLimitBytes())
	actual, _ := workspaceGCs.LoadOrStore(workspaceRoot, manager)
	return actual.(*workspacegc.Manager)
}

func workspaceCacheEnv(workspaceRoot string) []string {
	root := filepath.Join(filepath.Clean(workspaceRoot), "shared-cache")
	return []string{
		"GOCACHE=" + filepath.Join(root, "go-build"),
		"GOMODCACHE=" + filepath.Join(root, "go-mod"),
		"GOLANGCI_LINT_CACHE=" + filepath.Join(root, "go-lint"),
		"PIP_CACHE_DIR=" + filepath.Join(root, "python", "pip"),
		"PYTHONPYCACHEPREFIX=" + filepath.Join(root, "python", "pycache"),
		"npm_config_cache=" + filepath.Join(root, "node", "npm"),
		"PNPM_HOME=" + filepath.Join(root, "node", "pnpm"),
		"MAVEN_OPTS=-Dmaven.repo.local=" + filepath.Join(root, "maven"),
		"GRADLE_USER_HOME=" + filepath.Join(root, "gradle"),
	}
}

// CollectWorkspaceGarbage is used by startup and the periodic recovery sweep.
// A fresh process has no in-memory leases, so only explicitly managed generated
// roots are eligible for collection.
func CollectWorkspaceGarbage(workspaceRoot string) (int64, error) {
	return workspaceGCFor(workspaceRoot).CollectIfIdle(context.Background())
}
