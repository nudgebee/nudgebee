package handlers

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"nudgebee/code-analysis-agent/config"
	"nudgebee/code-analysis-agent/internal/workspacegc"
	"nudgebee/code-analysis-agent/tools"
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

// workspaceStoragePaths lists the directories that share the pod's
// ephemeral-storage budget. llm-server injects them from the pod's writable
// volumes (WORKSPACE_STORAGE_PATHS, separated like PATH) so the cleanup can
// measure the same total the kubelet enforces. Outside a workspace pod the
// variable is absent and only the workspace root is measured.
func workspaceStoragePaths() []string {
	return filepath.SplitList(os.Getenv("WORKSPACE_STORAGE_PATHS"))
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
	manager.SetUsageRoots(workspaceStoragePaths())
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

// execWorkspaceMaxIdle is how long a conversation's command folder may go
// unmodified before the idle sweep removes it. Later turns of a conversation
// read back the logs and evidence saved there, and those files cannot be
// rebuilt, so the age is generous: conversations are often resumed after a day
// and rarely after a week. The folders are small (under 1 MB each on average),
// so a week of them is a few hundred MB even on a busy workspace.
// llm-server lists saved files to the model for the same window
// (fsEvidenceIndexMaxAgeHours); that window must not be longer than this one.
const execWorkspaceMaxIdle = 7 * 24 * time.Hour

// execWorkspaceBaseDir is the directory holding one command folder per
// conversation. The command and file handlers and the cleanup all resolve it
// here so the sweep can never target a different directory than the one in use.
func execWorkspaceBaseDir(cfg *config.Config) string {
	if cfg.Execution.WorkspaceDir != "" {
		return cfg.Execution.WorkspaceDir
	}
	// Fallback to analysis workspace if execution workspace is not configured
	if cfg.Analysis.WorkspaceDir != "" {
		return filepath.Join(cfg.Analysis.WorkspaceDir, "exec_workspaces")
	}
	return "/tmp/code-analysis/exec_workspaces" // Hard default
}

// analysisWorkspacePrefix is the name every run checkout in the temp directory
// starts with: the per-conversation analysis workspace and the per-run PR
// follow-up clone.
const analysisWorkspacePrefix = "code-analysis-"

// RegisterWorkspaceLeftovers adds the locations no lease owns to the workspace
// cleanup: per-conversation command folders, the files holding spilled tool
// output, and run checkouts left in the temp directory. abandonedRunMaxAge
// should outlast the longest run, since a paused conversation reuses its
// checkout and a run reads its spill files back.
//
// Run checkouts used to be removed by a separate timer that looked only at the
// checkout's top-level timestamp and ran whether or not a run was active, so a
// turn starting shortly before the age was reached could lose its checkout
// mid-run. Here they are judged by the newest change anywhere inside them, and
// only while the workspace is idle.
func RegisterWorkspaceLeftovers(cfg *config.Config, abandonedRunMaxAge time.Duration) {
	gc := workspaceGCFor(cfg.Analysis.WorkspaceDir)
	execDir := execWorkspaceBaseDir(cfg)
	if abs, err := filepath.Abs(execDir); err == nil {
		execDir = abs
	}
	// A refused path means that location is never cleaned up; say so, since the
	// only other symptom is a disk that slowly fills.
	if !gc.AddAgedRoot(execDir, execWorkspaceMaxIdle) {
		slog.Warn("workspace cleanup: command folders will not be cleaned up; directory refused", "path", execDir)
	}
	if spillDir := tools.ToolOutputSpillDir(); !gc.AddAgedRoot(spillDir, abandonedRunMaxAge) {
		slog.Warn("workspace cleanup: spilled tool output will not be cleaned up; directory refused", "path", spillDir)
	}
	if tempDir := os.TempDir(); !gc.AddAgedChildren(tempDir, analysisWorkspacePrefix, abandonedRunMaxAge) {
		slog.Warn("workspace cleanup: abandoned run checkouts will not be cleaned up; directory refused", "path", tempDir)
	}
}

// CollectWorkspaceGarbage is used by startup and the periodic recovery sweep.
// A fresh process has no in-memory leases, so only explicitly managed generated
// roots are eligible for collection.
func CollectWorkspaceGarbage(workspaceRoot string) (int64, error) {
	return workspaceGCFor(workspaceRoot).CollectIfIdle(context.Background())
}
