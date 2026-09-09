package prompts

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	nbcommon "nudgebee/llm/common"
	"nudgebee/llm/config"
)

// The "default" tree holds the baseline prompts every deployment falls back
// to. The "models" tree holds per-model overrides, one subdirectory per
// model key (see modelResolutionBases) -- new model overrides need only a new
// directory under prompts/models/, never a change to this embed directive.
// models/.gitkeep guarantees the pattern always matches at least one file, so
// the build doesn't fail when no model override exists yet.
//
//go:embed all:default all:models
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
		dbManager, err := nbcommon.GetDatabaseManager(nbcommon.Metastore)
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

	// Normalize model name (lowercase, trim, and default to "default" if empty)
	// Use a separate variable to avoid mutating input parameter
	model := NormalizeModelName(req.Model)

	// Create normalized request for downstream use
	normalizedReq := req
	normalizedReq.Model = model

	// Validate request
	if err := l.validateRequest(normalizedReq); err != nil {
		return nil, err
	}

	// Check cache
	if cached, hit := l.cache.Get(normalizedReq); hit {
		cached.Metadata.LoadTimeMs = time.Since(startTime).Milliseconds()
		return cached, nil
	}

	// Resolve configuration (version + model + source)
	config, err := l.resolveConfig(ctx, normalizedReq)
	if err != nil {
		return nil, err
	}

	// Load prompt file. servedVersion is the version whose file was actually
	// matched -- may differ from config.Version when the requested version
	// (DB experiment/config, or a local PROMPTS_VERSION pin) has no file for
	// this specific prompt and the loader fell through to v1.
	content, servedVersion, err := l.loadPromptFile(normalizedReq.Name, normalizedReq.Category, config.Model, config.Version)
	if err != nil {
		return nil, err
	}
	if servedVersion != config.Version {
		// WARN for the local-dev pin specifically -- a developer needs to see
		// a typo'd PROMPTS_VERSION immediately. Any other source (DB
		// experiment/config naming a version some prompts don't have) logs
		// at Debug instead: at production scale, with many prompts/accounts
		// behind an hourly cache TTL, a WARN per cache-miss mismatch across
		// every affected prompt would be log spam, not a signal anyone acts
		// on -- the fallback itself is graceful and expected.
		logFn := slog.Debug
		if config.ConfigSource == ConfigSourceForcedDev {
			logFn = slog.Warn
		}
		logFn("prompts: requested version has no file for this prompt, served a fallback instead",
			"prompt", normalizedReq.Name,
			"requested_version", config.Version,
			"served_version", servedVersion,
			"config_source", config.ConfigSource)
	}

	// Build response
	response := &PromptResponse{
		Content: content,
		Metadata: PromptMetadata{
			Version:        servedVersion,
			Model:          config.Model,
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
	if req.Model == "" {
		return fmt.Errorf("model is required (should be normalized by caller)")
	}

	// Validate category
	validCategories := map[PromptCategory]bool{
		CategoryAgents:    true,
		CategoryPlanners:  true,
		CategoryTools:     true,
		CategoryUtilities: true,
		CategoryFragments: true,
	}
	if !validCategories[req.Category] {
		return fmt.Errorf("invalid category: %s", req.Category)
	}

	return nil
}

// resolveConfig resolves the configuration using the priority order:
// 1. Active Experiment
// 2. Database Configuration
// 3. Forced-version dev override (PROMPTS_VERSION / PROMPTS_VERSION_<NAME>)
// 4. Hardcoded Default (v1)
//
// (A "3. defaults.json" step was listed here previously; defaults.json has
// zero references anywhere in the tree, so that line was stale and is
// removed rather than replaced.)
//
// DB lookups (priorities 1-2) match on model twice: the exact resolved model
// string first, then its canonicalized form. This lets a stored row target
// either an exact deployment string ("qwen3-235b-vertex") or a canonical
// family name that also matches deployment-specific variants (a Bedrock
// cross-region id and its short name both canonicalize the same way).
func (l *PromptLoader) resolveConfig(ctx context.Context, req PromptRequest) (*ResolvedConfig, error) {
	canonicalModel := nbcommon.CanonicalModelID(req.Model)

	// Priority 1: Check for active experiment
	if l.db != nil && req.AccountID != "" {
		experiments, err := l.db.GetActiveExperiments(ctx, req.Name, req.Category, req.Model, canonicalModel, req.AccountID)
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
					"model", req.Model,
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
				Model:          req.Model,
				ConfigSource:   ConfigSourceExperiment,
				ExperimentID:   &exp.ID,
				ExperimentName: &exp.Name,
			}, nil
		}
	}

	// Priority 2: Check database configuration
	if l.db != nil {
		config, err := l.db.GetConfig(ctx, req.Name, req.Category, req.Model, canonicalModel, req.AccountID)
		if err != nil {
			slog.Warn("prompts: failed to check database config, continuing with fallback",
				"prompt", req.Name, "error", err)
		} else if config != nil {
			slog.Info("prompts: using database config",
				"prompt", req.Name,
				"version", config.ActiveVersion,
				"model", config.Model,
				"account_id", req.AccountID)

			return &ResolvedConfig{
				Version:      config.ActiveVersion,
				Model:        config.Model,
				ConfigSource: ConfigSourceDatabase,
			}, nil
		} else {
			slog.Info("prompts: no database config found",
				"prompt", req.Name,
				"category", req.Category,
				"model", req.Model,
				"account_id", req.AccountID)
		}
	} else {
		slog.Info("prompts: db not available, skipping database config",
			"prompt", req.Name)
	}

	// Priority 3: local-dev version pin. PROMPTS_VERSION_<PROMPT_NAME> takes
	// precedence over the global PROMPTS_VERSION, so one prompt can be
	// pinned independently of the rest -- e.g. hold k8s_lean at v1 while
	// testing react_3_base at v2, or pin down to an older version even when
	// a newer one exists on disk. Both are unset in any deployed
	// environment; production always falls through to Priority 4.
	if forced := forcedVersionFor(req.Name); forced != "" {
		slog.Info("prompts: using forced version (dev override)",
			"prompt", req.Name,
			"version", forced)

		return &ResolvedConfig{
			Version:      forced,
			Model:        req.Model,
			ConfigSource: ConfigSourceForcedDev,
		}, nil
	}

	// Priority 4: Hardcoded default
	slog.Info("prompts: falling back to hardcoded default",
		"prompt", req.Name,
		"version", "v1")

	return &ResolvedConfig{
		Version:      "v1",
		Model:        req.Model,
		ConfigSource: ConfigSourceDefault,
	}, nil
}

// forcedVersionFor returns the local-dev pinned version for a specific
// prompt name: PROMPTS_VERSION_<PROMPT_NAME> if set, else the global
// PROMPTS_VERSION, else "". Mirrors the LLM_PROVIDER_<AGENT> /
// LLM_PROVIDER per-agent-then-global env pattern in
// agents/core/llm_config.go. The per-prompt key is read dynamically via
// viper (AutomaticEnv, set up in config.init()) rather than a static
// struct field, since prompt names aren't enumerable in config.go.
// No registered prompt name has a hyphen today, but env vars can't carry
// one in most shells -- normalized so a future hyphenated name doesn't
// silently become impossible to pin.
func forcedVersionFor(promptName string) string {
	normalizedName := strings.ReplaceAll(strings.ToLower(promptName), "-", "_")
	key := fmt.Sprintf("prompts_version_%s", normalizedName)
	if v := config.Config.GetString(key, ""); v != "" {
		return v
	}
	// Read dynamically here too (not just config.Config.PromptsVersion,
	// which is a snapshot taken once at process startup) so the global
	// override is live for the same reason the per-prompt one already is,
	// and so tests can use t.Setenv instead of mutating the shared struct
	// field directly.
	if v := config.Config.GetString("prompts_version", ""); v != "" {
		return v
	}
	return config.Config.PromptsVersion
}

// includeRegex matches {{@include <path>}} directives in prompt content.
var includeRegex = regexp.MustCompile(`\{\{@include\s+([^}]+)\}\}`)

// maxIncludeDepth is the maximum recursion depth for nested includes.
const maxIncludeDepth = 3

// promptFileExtensions are tried at each resolution base. Only .yaml remains —
// every prompt is now defined by the schema in schema.go. The list is kept as a
// slice so an include can be written without an extension.
var promptFileExtensions = []string{".yaml"}

// promptFileBase is one entry in loadPromptFile's fallback chain: a path to
// try, paired with the version actually embedded in that path. Named
// (rather than two parallel slices) so a future edit to one base can't
// desync it from its version.
type promptFileBase struct {
	path    string
	version string
}

// loadPromptFile loads a prompt file from embedded FS with fallback logic.
// The returned version is the one actually matched (see promptFileBase),
// which the caller must use for response metadata -- it can differ from the
// requested version when that version has no file for this specific prompt.
func (l *PromptLoader) loadPromptFile(name string, category PromptCategory, model string, version string) (string, string, error) {
	// Try bases in order, most specific first:
	// 1. models/{model}/{version}/{category}/{name}           -- exact resolved model
	// 2. models/{canonicalModel}/{version}/{category}/{name}  -- only if it differs from {model}
	// 3. default/{version}/{category}/{name}                  -- only if {model} isn't already "default"
	// then the same three again pinned to v1 if {version} != "v1".
	//
	// processIncludes must resolve each file's @include directives against
	// the version it was actually FOUND at, not the originally-requested
	// one: a match on the v1 fallback whose body still has an unresolved
	// {{@include ...}} would otherwise look for that fragment under the
	// requested version's directory (e.g. an experiment/DB-config/dev-pin
	// naming a version that has no matching fragment files) and fail a load
	// that the v1 baseline -- guaranteed include-clean by MustResolveAll --
	// would have served just fine.
	modelBases := modelResolutionBases(model)

	var bases []promptFileBase
	for _, base := range modelBases {
		bases = append(bases, promptFileBase{fmt.Sprintf("%s/%s/%s/%s", base, version, category, name), version})
	}
	// The v1-pinned repeat is identical to the tier above when version is
	// already "v1" -- the overwhelmingly common case, since every prompt
	// without an experiment/DB-config/dev-pin resolves through the
	// hardcoded v1 default. Skip the duplicates rather than probing the
	// same paths twice.
	if version != "v1" {
		for _, base := range modelBases {
			bases = append(bases, promptFileBase{fmt.Sprintf("%s/v1/%s/%s", base, category, name), "v1"})
		}
	}

	// recordErr keeps the most useful failure rather than the most recent one. Paths are
	// walked most-specific first, so a malformed override or a broken include explains the
	// problem; the "does not exist" errors that follow from probing the rest of the chain
	// are expected noise and must not overwrite it.
	var lastErr error
	recordErr := func(err error) {
		if lastErr == nil || errors.Is(lastErr, fs.ErrNotExist) {
			lastErr = err
		}
	}

	for _, base := range bases {
		for _, ext := range promptFileExtensions {
			path := base.path + ext
			rawContent, err := fs.ReadFile(l.fs, path)
			if err != nil {
				recordErr(err)
				continue
			}

			slog.Debug("prompts: loaded file",
				"prompt", name,
				"path", path)

			var body string
			{
				promptFile, parseErr := ParsePromptFile(rawContent)
				if parseErr != nil {
					// Treat a malformed file the same as a missing one and keep walking
					// the resolution chain, which ends at default/v1 — the baseline
					// MustResolveAll validated at startup.
					//
					// The alternative is returning "" to the caller, which hands an agent
					// an empty system prompt: strictly worse than serving the validated
					// baseline. This is not the silent fallback this package was built to
					// remove — that one hid a whole dead system behind a Debug line. This
					// logs at ERROR with the offending path so a broken override is
					// visible while the service keeps answering.
					slog.Error("prompts: malformed prompt file, falling through to the next resolution path",
						"prompt", name, "path", path, "error", parseErr)
					recordErr(parseErr)
					continue
				}
				body = promptFile.Body
			}

			// Process {{@include ...}} directives before returning. A broken include
			// makes the file as unusable as a malformed one above, so it falls through
			// the same way rather than failing the whole load: an override pointing at
			// a missing fragment must not take down a prompt whose default/v1 baseline
			// is intact. MustResolveAll runs this same path over default/v1 at startup,
			// so the end of the chain is guaranteed include-clean.
			content, err := l.processIncludes(body, model, base.version, 0)
			if err != nil {
				slog.Error("prompts: failed to process includes, falling through to the next resolution path",
					"prompt", name, "path", path, "error", err)
				recordErr(err)
				continue
			}

			// Replace identity placeholders with configured values
			content = replaceIdentityPlaceholders(content)

			return content, base.version, nil
		}
	}

	return "", "", fmt.Errorf("prompt not found: %s (category: %s, model: %s, version: %s): %w",
		name, category, model, version, lastErr)
}

// modelFamilyPatterns maps a substring found in a (lowercased) model name to
// the shared family override folder for it. Ordered; first match wins.
// Deliberately coarser than the exact/canonical tiers in modelResolutionBases
// -- see modelFamily. Mirrors the same "match the whole model line, not one
// deployment string" pattern already used for token-limit lookup
// (agents/core/llm_tokencount.go's strings.Contains(n, "qwen") case).
var modelFamilyPatterns = []struct {
	substr string
	family string
}{
	{"qwen", "qwen"},
}

// modelFamily returns the shared family override folder for model (e.g.
// "qwen" for any of "qwen3-235b-vertex", "qwen/qwen3-vl-235b-a22b-instruct",
// "Qwen/Qwen3.6-35B-A3B-FP8"), or "" if no known family matches.
func modelFamily(model string) string {
	lower := strings.ToLower(model)
	for _, p := range modelFamilyPatterns {
		if strings.Contains(lower, p.substr) {
			return p.family
		}
	}
	return ""
}

// modelResolutionBases returns the ordered, deduplicated list of embedded-FS
// directory prefixes to try for a given resolved model: the exact model
// string under models/, then its canonicalized form (only if different),
// then its family override (only if different from both -- see modelFamily),
// then the shared "default" tree (only if model isn't already "default").
// Shared by loadPromptFile and processIncludes so a file and its fragment
// includes resolve through the same specificity ladder. A model override
// lives at "models/<model>/<version>/...", never at the tree root, so new
// overrides never need a change to the //go:embed directive above.
func modelResolutionBases(model string) []string {
	// "default" IS the shared tree, not a model override to look up under
	// models/ -- the overwhelmingly common case (no override configured for
	// this request) must resolve straight to the real default/ root. Callers
	// normalize via NormalizeModelName before reaching here today, so "" never
	// actually arrives -- guarded anyway since this is a shared internal
	// helper with several call sites, one caller forgetting to normalize
	// shouldn't construct a bogus "models/" lookup.
	if model == "" || model == "default" {
		return []string{"default"}
	}
	bases := []string{"models/" + model}
	// Pre-seeded with "default": canonical/family are free-form derivations
	// of an arbitrary model string, so nothing stops one from coincidentally
	// equaling "default" -- without this, that would append a nonsensical
	// "models/default" probe (default/ is never nested under models/).
	seen := map[string]bool{model: true, "default": true}

	if canonical := nbcommon.CanonicalModelID(model); canonical != "" && !seen[canonical] {
		bases = append(bases, "models/"+canonical)
		seen[canonical] = true
	}
	if family := modelFamily(model); family != "" && !seen[family] {
		bases = append(bases, "models/"+family)
		seen[family] = true
	}

	bases = append(bases, "default")
	return bases
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
// Include paths are resolved through the same model-key ladder as loadPromptFile
// (exact model → canonical model → default).
// Recursion is limited to maxIncludeDepth to prevent infinite loops.
func (l *PromptLoader) processIncludes(content string, model string, version string, depth int) (string, error) {
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

		// Try bases in order: models/{model}/version, models/{canonicalModel}/version,
		// then default/version. Each is tried verbatim first (legacy includes such as
		// `_persona/nubi_persona.txt` carry their own extension) and then with the
		// prompt-file extensions appended, so a fragment can be referenced as
		// `_fragments/time_handling_rules`.
		var paths []string
		for _, modelBase := range modelResolutionBases(model) {
			base := fmt.Sprintf("%s/%s/%s", modelBase, version, includePath)
			paths = append(paths, base)
			for _, ext := range promptFileExtensions {
				paths = append(paths, base+ext)
			}
		}

		var includeBody string
		var found bool
		var lastErr error
		for _, path := range paths {
			data, err := fs.ReadFile(l.fs, path)
			if err != nil {
				lastErr = err
				continue
			}

			// Include paths are tried verbatim before extensions are appended, so this
			// path is not guaranteed to be YAML and the raw bytes are the right value
			// when it is not. Detect the YAML case case-insensitively and accept .yml:
			// misclassifying a prompt file as raw text leaks its apiVersion/name header
			// into the rendered prompt and ships it to the model.
			includeBody = string(data)
			if lower := strings.ToLower(path); strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") {
				fragment, parseErr := ParsePromptFile(data)
				if parseErr != nil {
					processErr = fmt.Errorf("parsing include %s: %w", path, parseErr)
					return match
				}
				includeBody = fragment.Body
			}

			found = true
			slog.Debug("prompts: resolved include",
				"include", includePath,
				"path", path)
			break
		}

		if !found {
			processErr = fmt.Errorf("include file not found: %s (tried: %v): %w",
				includePath, paths, lastErr)
			return match
		}

		// Recursively process includes in the included content
		resolved, err := l.processIncludes(includeBody, model, version, depth+1)
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
			req.Model,
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
		Model:          req.Model,
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

// GetAvailableVersions returns all available versions for a prompt, checking
// the model's own directory (exact, then canonical) plus default.
func (l *PromptLoader) GetAvailableVersions(name string, category PromptCategory, model string) []string {
	versions := make(map[string]bool)
	normalizedModel := NormalizeModelName(model)

	for _, base := range modelResolutionBases(normalizedModel) {
		entries, err := fs.ReadDir(l.fs, base)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() && strings.HasPrefix(entry.Name(), "v") {
				// Check if the prompt file exists in this version, in any supported format
				for _, ext := range promptFileExtensions {
					filePath := fmt.Sprintf("%s/%s/%s/%s%s", base, entry.Name(), category, name, ext)
					if _, err := fs.Stat(l.fs, filePath); err == nil {
						versions[entry.Name()] = true
						break
					}
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
