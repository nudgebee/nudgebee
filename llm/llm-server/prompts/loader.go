package prompts

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"nudgebee/llm/common"
	"nudgebee/llm/config"
)

//go:embed all:default
var embeddedFS embed.FS

var (
	globalLoader     *PromptLoader
	globalLoaderOnce sync.Once
)

// PromptLoader manages prompt loading with versioning and caching
type PromptLoader struct {
	db    *PromptDB
	cache *PromptCache
	fs    fs.FS
}

// InitializeGlobalLoader initializes the global prompt loader.
// This should be called once during application startup.
// It never returns an error: DB unavailability is non-fatal (loader falls back
// to embedded FS), and the embedded FS always succeeds.
func InitializeGlobalLoader() {
	globalLoaderOnce.Do(func() {
		// Initialize metrics first
		InitMetrics()

		// Get database manager — unavailability is non-fatal, loader works from embedded FS
		dbManager, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			slog.Warn("prompts: failed to get database manager, running without DB", "error", err)
			dbManager = nil
		}

		var promptDB *PromptDB
		if dbManager != nil {
			promptDB = NewPromptDB(dbManager)
			if !promptDB.IsAvailable() {
				slog.Warn("prompts: database not available, running without DB")
				promptDB = nil
			}
		}

		// Create cache with 1 hour TTL
		cache := NewPromptCache(1 * time.Hour)

		globalLoader = &PromptLoader{
			db:    promptDB,
			cache: cache,
			fs:    embeddedFS,
		}

		slog.Info("prompts: global loader initialized",
			"has_db", promptDB != nil,
			"cache_ttl", "1h")
	})
}

// GetLoader returns the global prompt loader instance
// Initializes lazily if not already initialized (useful for tests)
func GetLoader() *PromptLoader {
	InitializeGlobalLoader()
	return globalLoader
}

// NewLoaderForTesting returns a PromptLoader backed by the embedded FS with no DB.
// Use in conjunction with SetGlobalLoaderForTesting to initialize the global loader in tests.
func NewLoaderForTesting() *PromptLoader {
	return &PromptLoader{
		db:    nil,
		cache: NewPromptCache(1 * time.Hour),
		fs:    embeddedFS,
	}
}

// SetGlobalLoaderForTesting replaces the global loader singleton.
// Must only be called from test code.
func SetGlobalLoaderForTesting(l *PromptLoader) {
	globalLoader = l
}

// GetPrompt loads a prompt with full resolution logic
// Accepts a context to respect upstream timeouts and cancellation for database queries
func (l *PromptLoader) GetPrompt(ctx context.Context, req PromptRequest) (*PromptResponse, error) {
	startTime := time.Now()

	// Normalize provider to "default" if empty
	// Use a separate variable to avoid mutating input parameter
	provider := req.Provider
	if provider == "" {
		provider = "default"
	}

	// Create normalized request for downstream use
	normalizedReq := req
	normalizedReq.Provider = provider

	// Validate request
	if err := l.validateRequest(normalizedReq); err != nil {
		return nil, err
	}

	// Check cache
	if cached, hit := l.cache.Get(normalizedReq); hit {
		cached.Metadata.LoadTimeMs = time.Since(startTime).Milliseconds()
		return cached, nil
	}

	// Resolve configuration (version + provider + source)
	config, err := l.resolveConfig(ctx, normalizedReq)
	if err != nil {
		return nil, err
	}

	// Load prompt file
	content, err := l.loadPromptFile(normalizedReq.Name, normalizedReq.Category, config.Provider, config.Version)
	if err != nil {
		return nil, err
	}

	// Build response
	response := &PromptResponse{
		Content: content,
		Metadata: PromptMetadata{
			Version:        config.Version,
			Provider:       config.Provider,
			Category:       normalizedReq.Category,
			ConfigSource:   config.ConfigSource,
			ExperimentID:   config.ExperimentID,
			ExperimentName: config.ExperimentName,
			CacheHit:       false,
			LoadTimeMs:     time.Since(startTime).Milliseconds(),
		},
	}

	// Cache the response
	l.cache.Set(normalizedReq, response)

	// Record metrics asynchronously (don't block)
	go l.recordMetrics(normalizedReq, response, nil)

	return response, nil
}

// validateRequest validates the prompt request
func (l *PromptLoader) validateRequest(req PromptRequest) error {
	if req.Name == "" {
		return fmt.Errorf("prompt name is required")
	}
	if req.Category == "" {
		return fmt.Errorf("category is required")
	}
	if req.Provider == "" {
		return fmt.Errorf("provider is required (should be normalized by caller)")
	}

	// Validate category
	validCategories := map[PromptCategory]bool{
		CategoryAgents:    true,
		CategoryPlanners:  true,
		CategoryTools:     true,
		CategoryUtilities: true,
	}
	if !validCategories[req.Category] {
		return fmt.Errorf("invalid category: %s", req.Category)
	}

	return nil
}

// resolveConfig resolves the configuration using the priority order:
// 1. Active Experiment
// 2. Database Configuration
// 3. defaults.json
// 4. Hardcoded Default (v1)
func (l *PromptLoader) resolveConfig(ctx context.Context, req PromptRequest) (*ResolvedConfig, error) {
	// Priority 1: Check for active experiment
	if l.db != nil && req.AccountID != "" {
		experiments, err := l.db.GetActiveExperiments(ctx, req.Name, req.Category, req.Provider, req.AccountID)
		if err != nil {
			slog.Warn("prompts: failed to check experiments, continuing with fallback",
				"error", err)
		} else if len(experiments) > 0 {
			// Warn if multiple experiments are active (ambiguous priority)
			if len(experiments) > 1 {
				expNames := make([]string, len(experiments))
				for i, e := range experiments {
					expNames[i] = e.Name
				}
				slog.Warn("prompts: multiple active experiments found, using most recent",
					"prompt", req.Name,
					"account", req.AccountID,
					"provider", req.Provider,
					"count", len(experiments),
					"experiments", expNames,
					"selected", experiments[0].Name)
			}

			// Use the first matching experiment (most recently created by ORDER BY created_at DESC)
			exp := experiments[0]
			slog.Info("prompts: using experiment",
				"prompt", req.Name,
				"experiment", exp.Name,
				"version", exp.TestVersion,
				"account", req.AccountID)

			return &ResolvedConfig{
				Version:        exp.TestVersion,
				Provider:       req.Provider,
				ConfigSource:   ConfigSourceExperiment,
				ExperimentID:   &exp.ID,
				ExperimentName: &exp.Name,
			}, nil
		}
	}

	// Priority 2: Check database configuration
	if l.db != nil {
		config, err := l.db.GetConfig(ctx, req.Name, req.Category, req.Provider, req.AccountID)
		if err != nil {
			slog.Warn("prompts: failed to check database config, continuing with fallback",
				"prompt", req.Name, "error", err)
		} else if config != nil {
			slog.Info("prompts: using database config",
				"prompt", req.Name,
				"version", config.ActiveVersion,
				"provider", config.Provider,
				"account_id", req.AccountID)

			return &ResolvedConfig{
				Version:      config.ActiveVersion,
				Provider:     config.Provider,
				ConfigSource: ConfigSourceDatabase,
			}, nil
		} else {
			slog.Info("prompts: no database config found",
				"prompt", req.Name,
				"category", req.Category,
				"provider", req.Provider,
				"account_id", req.AccountID)
		}
	} else {
		slog.Info("prompts: db not available, skipping database config",
			"prompt", req.Name)
	}

	// Priority 3: Hardcoded default
	slog.Info("prompts: falling back to hardcoded default",
		"prompt", req.Name,
		"version", "v1")

	return &ResolvedConfig{
		Version:      "v1",
		Provider:     req.Provider,
		ConfigSource: ConfigSourceDefault,
	}, nil
}

// includeRegex matches {{@include <path>}} directives in prompt content.
var includeRegex = regexp.MustCompile(`\{\{@include\s+([^}]+)\}\}`)

// maxIncludeDepth is the maximum recursion depth for nested includes.
const maxIncludeDepth = 3

// loadPromptFile loads a prompt file from embedded FS with fallback logic
func (l *PromptLoader) loadPromptFile(name string, category PromptCategory, provider string, version string) (string, error) {
	// Try paths in order:
	// 1. {provider}/{version}/{category}/{name}.txt
	// 2. default/{version}/{category}/{name}.txt
	// 3. {provider}/v1/{category}/{name}.txt
	// 4. default/v1/{category}/{name}.txt

	paths := []string{
		fmt.Sprintf("%s/%s/%s/%s.txt", provider, version, category, name),
		fmt.Sprintf("default/%s/%s/%s.txt", version, category, name),
		fmt.Sprintf("%s/v1/%s/%s.txt", provider, category, name),
		fmt.Sprintf("default/v1/%s/%s.txt", category, name),
	}

	var lastErr error
	for _, path := range paths {
		rawContent, err := fs.ReadFile(l.fs, path)
		if err == nil {
			slog.Debug("prompts: loaded file",
				"prompt", name,
				"path", path)

			// Process {{@include ...}} directives before returning
			content, err := l.processIncludes(string(rawContent), provider, version, 0)
			if err != nil {
				return "", fmt.Errorf("processing includes for %s: %w", path, err)
			}

			// Replace identity placeholders with configured values
			content = replaceIdentityPlaceholders(content)

			return content, nil
		}
		lastErr = err
	}

	return "", fmt.Errorf("prompt not found: %s (category: %s, provider: %s, version: %s): %w",
		name, category, provider, version, lastErr)
}

// replaceIdentityPlaceholders substitutes {{@assistant_name}} and {{@assistant_company}}
// with the configured AI assistant identity values, enabling white-labeling of prompts.
func replaceIdentityPlaceholders(content string) string {
	content = strings.ReplaceAll(content, "{{@assistant_name}}", config.Config.AIAssistantName)
	content = strings.ReplaceAll(content, "{{@assistant_company}}", config.Config.AIAssistantCompany)
	return content
}

// processIncludes resolves {{@include <relative_path>}} directives in prompt content.
// It replaces each directive with the content of the referenced file from the embedded FS.
// Include paths are resolved with provider/version fallback (provider → default).
// Recursion is limited to maxIncludeDepth to prevent infinite loops.
func (l *PromptLoader) processIncludes(content string, provider string, version string, depth int) (string, error) {
	if depth >= maxIncludeDepth {
		return "", fmt.Errorf("include depth limit exceeded (%d)", maxIncludeDepth)
	}

	if !includeRegex.MatchString(content) {
		return content, nil
	}

	var processErr error
	result := includeRegex.ReplaceAllStringFunc(content, func(match string) string {
		if processErr != nil {
			return match
		}

		submatches := includeRegex.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}

		includePath := strings.TrimSpace(submatches[1])

		// Try paths in order: provider/version, then default/version
		paths := []string{
			fmt.Sprintf("%s/%s/%s", provider, version, includePath),
			fmt.Sprintf("default/%s/%s", version, includePath),
		}

		var includeContent []byte
		var lastErr error
		for _, path := range paths {
			data, err := fs.ReadFile(l.fs, path)
			if err == nil {
				includeContent = data
				slog.Debug("prompts: resolved include",
					"include", includePath,
					"path", path)
				break
			}
			lastErr = err
		}

		if includeContent == nil {
			processErr = fmt.Errorf("include file not found: %s (tried: %v): %w",
				includePath, paths, lastErr)
			return match
		}

		// Recursively process includes in the included content
		resolved, err := l.processIncludes(string(includeContent), provider, version, depth+1)
		if err != nil {
			processErr = err
			return match
		}

		return resolved
	})

	if processErr != nil {
		return "", processErr
	}

	return result, nil
}

// recordMetrics records metrics asynchronously (both database and OpenTelemetry)
func (l *PromptLoader) recordMetrics(req PromptRequest, resp *PromptResponse, err error) {
	// Recover from panics - metrics recording should never crash the app
	defer func() {
		if r := recover(); r != nil {
			slog.Warn("prompts: metrics recording panicked (non-critical)",
				"panic", r,
				"prompt", req.Name)
		}
	}()
	// Record OpenTelemetry metrics (always)
	if err != nil {
		RecordPromptError(req.Name, string(req.Category), "load_failed")
	} else {
		experimentName := ""
		if resp.Metadata.ExperimentName != nil {
			experimentName = *resp.Metadata.ExperimentName
		}

		RecordPromptLoad(
			req.Name,
			string(req.Category),
			req.Provider,
			resp.Metadata.Version,
			float64(resp.Metadata.LoadTimeMs)/1000.0,
			resp.Metadata.CacheHit,
			string(resp.Metadata.ConfigSource),
			experimentName,
			req.AccountID,
		)
	}

	// Record database metrics (if available)
	if l.db == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	experimentName := resp.Metadata.ExperimentName

	var accountID *string
	if req.AccountID != "" {
		accountID = &req.AccountID
	}

	loadTimeMs := int(resp.Metadata.LoadTimeMs)
	cacheHit := resp.Metadata.CacheHit
	configSource := string(resp.Metadata.ConfigSource)

	metrics := &DBMetrics{
		PromptName:     req.Name,
		Category:       req.Category,
		Provider:       req.Provider,
		Version:        resp.Metadata.Version,
		AccountID:      accountID,
		LoadTimeMs:     &loadTimeMs,
		CacheHit:       &cacheHit,
		ConfigSource:   &configSource,
		ExperimentID:   resp.Metadata.ExperimentID,
		ExperimentName: experimentName,
		Error:          err != nil,
	}

	if err != nil {
		errMsg := err.Error()
		metrics.ErrorMessage = &errMsg
	}

	_ = l.db.RecordMetrics(ctx, metrics)
}

// ClearCache clears all cached prompts
func (l *PromptLoader) ClearCache() {
	l.cache.Clear()
	RecordCacheOperation("*", "*", "clear_all")
	slog.Info("prompts: cache cleared")
}

// ClearCacheForPrompt clears cache for a specific prompt
func (l *PromptLoader) ClearCacheForPrompt(name string, category PromptCategory) {
	l.cache.ClearByPrompt(name, category)
	RecordCacheOperation(name, string(category), "clear_prompt")
	slog.Info("prompts: cache cleared for prompt", "prompt", name, "category", category)
}

// ClearCacheForAccount clears cache for a specific account
func (l *PromptLoader) ClearCacheForAccount(accountID string) {
	l.cache.ClearByAccount(accountID)
	RecordCacheOperation("*", "*", "clear_account")
	slog.Info("prompts: cache cleared for account", "account_id", accountID)
}

// GetDB returns the database instance (for admin operations)
func (l *PromptLoader) GetDB() *PromptDB {
	return l.db
}

// GetCacheSize returns the current cache size
func (l *PromptLoader) GetCacheSize() int {
	return l.cache.Size()
}

// GetAvailableVersions returns all available versions for a prompt
func (l *PromptLoader) GetAvailableVersions(name string, category PromptCategory, provider string) []string {
	versions := make(map[string]bool)

	// Check provider-specific versions
	providerPath := provider
	entries, err := fs.ReadDir(l.fs, providerPath)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "v") {
				// Check if the prompt file exists in this version
				filePath := fmt.Sprintf("%s/%s/%s/%s.txt", providerPath, entry.Name(), category, name)
				if _, err := fs.Stat(l.fs, filePath); err == nil {
					versions[entry.Name()] = true
				}
			}
		}
	}

	// Check default versions
	defaultPath := "default"
	entries, err = fs.ReadDir(l.fs, defaultPath)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "v") {
				// Check if the prompt file exists in this version
				filePath := fmt.Sprintf("%s/%s/%s/%s.txt", defaultPath, entry.Name(), category, name)
				if _, err := fs.Stat(l.fs, filePath); err == nil {
					versions[entry.Name()] = true
				}
			}
		}
	}

	// Convert to sorted slice
	result := make([]string, 0, len(versions))
	for v := range versions {
		result = append(result, v)
	}

	return result
}

// SplitPromptLines splits prompt content into lines, handling empty content correctly.
// Returns an empty slice if content is empty or contains only whitespace.
// Otherwise, trims whitespace and splits by newline.
func SplitPromptLines(content string) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return []string{}
	}
	return strings.Split(content, "\n")
}
