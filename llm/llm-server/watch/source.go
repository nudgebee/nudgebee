package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Source observes a watched resource and produces an Observation. The
// abstraction lets new kinds (sql, timer, listen, webhook, composite) plug
// in without changing the executor or the persistence layer.
//
// Implementations must be safe for concurrent use; the dispatcher fans out
// polls across a worker pool.
type Source interface {
	Kind() SourceKind
	// ValidateConfig parses the JSON config blob and returns an error if it
	// is missing required fields or otherwise invalid. Called at create
	// time so bad configs fail fast.
	ValidateConfig(rawCfg json.RawMessage) error
	// Observe runs one poll. Returns the observation text plus an error if
	// the source itself failed (network, missing tool, bad SQL). Predicate
	// evaluation is the executor's responsibility.
	Observe(ctx *security.RequestContext, w Watch) (Observation, error)
}

var (
	sourcesMu sync.RWMutex
	sources   = map[SourceKind]Source{}
)

// RegisterSource installs a source implementation. Registration is
// idempotent within a process; re-registering the same kind overrides the
// previous one (used for tests).
func RegisterSource(s Source) {
	sourcesMu.Lock()
	defer sourcesMu.Unlock()
	sources[s.Kind()] = s
}

// GetSource looks up a registered source by kind.
func GetSource(kind SourceKind) (Source, bool) {
	sourcesMu.RLock()
	defer sourcesMu.RUnlock()
	s, ok := sources[kind]
	return s, ok
}

func init() {
	RegisterSource(&toolSource{})
	RegisterSource(&sqlSource{})
}

// ---------------------------------------------------------------------------
// tool source
// ---------------------------------------------------------------------------

// toolSourceConfig is the JSON shape stored in source_config when
// source_kind = "tool". Each poll re-invokes the named NBTool with the
// stored arguments. Tools used here MUST be idempotent and read-only — the
// watch contract is "observe, don't mutate."
type toolSourceConfig struct {
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	// Command optionally overrides NBToolCallRequest.Command; many tools
	// route on this field (e.g. "shell_execute" with a command string).
	Command string `json:"command,omitempty"`
	// ToolConfigName lets the agent disambiguate when the tenant has
	// multiple configurations for the same tool (e.g. several GitHub App
	// installs). When empty, Observe picks the first available enabled
	// config for the account. Mirroring the chat planner's
	// `nb_tool_config_name` resolution.
	ToolConfigName string `json:"tool_config_name,omitempty"`
}

// UnmarshalJSON tolerates the LLM's most common shape mistake: emitting
// `tool_input` as a bare string ("gh run view …") instead of an object
// ({"command": "gh run view …"}). Observed empirically — even when the
// description is explicit, models often output the string form for
// shell-style tools (gh, kubectl, shell_execute). Auto-wrapping as
// `{"command": <string>}` lets the watch register on first try instead
// of burning 4+ ReAct retries on a "cannot unmarshal string into Go
// struct field" error.
func (c *toolSourceConfig) UnmarshalJSON(data []byte) error {
	// Decode into a permissive intermediate first so we can inspect
	// tool_input's actual JSON kind before binding it.
	var raw struct {
		ToolName       string          `json:"tool_name"`
		ToolInput      json.RawMessage `json:"tool_input"`
		Command        string          `json:"command,omitempty"`
		ToolConfigName string          `json:"tool_config_name,omitempty"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	c.ToolName = raw.ToolName
	c.Command = raw.Command
	c.ToolConfigName = raw.ToolConfigName

	if len(raw.ToolInput) == 0 {
		c.ToolInput = nil
		return nil
	}

	// First, try the canonical object form.
	var asMap map[string]any
	if err := json.Unmarshal(raw.ToolInput, &asMap); err == nil {
		c.ToolInput = asMap
		return nil
	}

	// Fall back: a bare string. Wrap as {"command": <string>}. This is the
	// shape every shell-style tool's Call() actually consumes.
	var asString string
	if err := json.Unmarshal(raw.ToolInput, &asString); err == nil {
		c.ToolInput = map[string]any{"command": asString}
		return nil
	}

	// Anything else (number, array, bool) is a real shape error — surface
	// it to the agent so it can correct.
	return fmt.Errorf("tool source: tool_input must be an object or a string command, got %s", string(raw.ToolInput))
}

type toolSource struct{}

func (toolSource) Kind() SourceKind { return SourceKindTool }

func (toolSource) ValidateConfig(rawCfg json.RawMessage) error {
	var cfg toolSourceConfig
	if err := json.Unmarshal(rawCfg, &cfg); err != nil {
		return fmt.Errorf("tool source: invalid config json: %w", err)
	}
	if cfg.ToolName == "" {
		return fmt.Errorf("tool source: tool_name is required")
	}
	// Reject agent-style tools (NBToolTypeAgent). Watch polls run as a
	// stateless background task with no parent message — agent tools
	// load conversation history via WHERE id = $messageId, which fails
	// for polls. Force the LLM to use the underlying primitive *_execute
	// tool (kubectl_execute, github_execute, etc.) instead. The tool
	// description carries the same guidance; this is the hard guard.
	//
	// We look up the tool with an empty accountId because at validate
	// time we don't have one wired through the Source interface. Most
	// tool factories ignore accountId for type introspection — and the
	// type is what we need to inspect, not the account-bound config.
	if t, ok := toolcore.GetNBTool("", cfg.ToolName); ok && t != nil && t.GetType() == toolcore.NBToolTypeAgent {
		return fmt.Errorf("tool source: %q is an agent-style tool; watch sources must use a primitive *_execute tool (e.g. kubectl_execute, github_execute, shell_execute) — agent tools assume a parent message context that watch polls do not have", cfg.ToolName)
	}
	// Deny-list common destructive verbs in the source command. A watch
	// re-runs its source every poll_interval_sec under a static admin
	// context with no human in the loop, so an accidentally-registered
	// mutating watch amplifies its blast radius. This is a fail-fast
	// nudge — not a complete sandbox; the "must be idempotent and
	// read-only" contract in the tool description still stands and the
	// source command can always be obfuscated past a regex. The check
	// catches the obvious mistakes (`kubectl delete`, `aws … delete-…`,
	// `helm uninstall`, `rm -rf`, `gh release delete`).
	if cmd := pickCommandFromConfig(&cfg); cmd != "" {
		if verb, hit := looksMutating(cmd); hit {
			return fmt.Errorf("tool source: command %q starts with a mutating verb %q — watch sources must be idempotent and read-only (the poll re-runs this command every poll_interval_sec). For an outcome check, pick a read-only command like `kubectl rollout status`, `kubectl get`, `kubectl wait`, `gh run view`, or `aws … describe-…`", truncate(cmd, 120), verb)
		}
	}
	return nil
}

func (toolSource) Observe(ctx *security.RequestContext, w Watch) (Observation, error) {
	var cfg toolSourceConfig
	if err := json.Unmarshal([]byte(w.SourceConfig), &cfg); err != nil {
		return Observation{}, fmt.Errorf("tool source: corrupted config: %w", err)
	}
	tool, ok := toolcore.GetNBTool(w.AccountID.String(), cfg.ToolName)
	if !ok || tool == nil {
		return Observation{}, fmt.Errorf("tool source: tool %q not registered for account", cfg.ToolName)
	}

	// Defense-in-depth agent-type reject. ValidateConfig also rejects
	// NBToolTypeAgent sources at registration time, but it has no account
	// context (the Source interface doesn't carry one), so it cannot see
	// account-scoped custom tools — those would only fail here at observe
	// time, accumulating WatchMaxFailures of noise before terminating as
	// EXPIRED/FAILED. Doing the same check at observe time with the real
	// account ID catches custom agent-style tools too and surfaces them
	// as a single clean failure on poll #1 instead of repeated retries.
	if tool.GetType() == toolcore.NBToolTypeAgent {
		return Observation{}, fmt.Errorf("tool source: %q is an agent-style tool; watch sources must use a primitive *_execute tool (e.g. kubectl_execute, github_execute, shell_execute) — agent tools assume a parent message context that watch polls do not have", cfg.ToolName)
	}

	// Resolve which tool config the poll should use. The chat planner does
	// this through followupForMultipleToolConfigs (which can prompt the
	// user); the watch path runs unattended, so we use a deterministic
	// rule:
	//   1) If source_config.tool_config_name is set, use it verbatim.
	//   2) If the tool implements NBToolConfig and exactly one enabled
	//      config exists for the account, NewNbToolContext picks it
	//      automatically (existing behaviour).
	//   3) If multiple exist, pick the first by name and pass the name
	//      via QueryConfig.ToolConfigs so NewNbToolContext binds it.
	// Without this, tools like github_execute (35 tenant integrations,
	// 3 enabled) leave ToolConfig empty and every poll fails with
	// "no tool configs found".
	queryConfig := toolcore.NBQueryConfig{}
	if cfg.ToolConfigName != "" {
		queryConfig.ToolConfigs = map[string]string{cfg.ToolName: cfg.ToolConfigName}
	} else if _, isCfg := tool.(toolcore.NBToolConfig); isCfg {
		availableConfigs, err := toolcore.ListToolConfigs(ctx, w.AccountID.String(), tool)
		if err == nil && len(availableConfigs) > 1 {
			queryConfig.ToolConfigs = map[string]string{cfg.ToolName: availableConfigs[0].Name}
			ctx.GetLogger().Info("watch.source: multiple tool configs available, picking first",
				"tool", cfg.ToolName, "picked", availableConfigs[0].Name, "candidates", len(availableConfigs))
		}
	}

	// Conversation/Message IDs both default to the watch's own UUID rather
	// than empty string. Two consumers of these fields fail on empty input
	// in conflicting ways:
	//   - The workspace pod requires a non-empty string and validates against
	//     ^[a-zA-Z0-9_\-]+$ (workspace dirs are partitioned by conversation_id).
	//     Earlier we experimented with "watch-<uuid>" but the second consumer
	//     below rejects that.
	//   - Several agent-type tools (notably the `kubectl` ReAct sub-agent)
	//     query llm_conversation_messages via `WHERE conversation_id = $1`
	//     and `WHERE id = $messageId`. The schema types both columns as UUID,
	//     so any non-UUID string trips `pq: invalid input syntax for type uuid`.
	//
	// Using the raw watch UUID satisfies both: it's a valid uuid and matches
	// the workspace-pod regex. Lookups against the conversation_messages table
	// return zero rows (no real conversation has this UUID), which is the
	// correct outcome — a watch poll runs as a stateless background task with
	// no prior history.
	//
	// INVARIANT: every tool reachable as a `source_kind=tool` watch source
	// MUST be read-only with respect to llm_conversation_* tables. If a
	// tool's Call path inserts a row keyed on conversation_id / message_id,
	// that row will reference a UUID that does not exist in
	// llm_conversations — at best a FK violation surfacing as a noisy poll
	// failure, at worst silent orphan rows. The agent-type reject in
	// ValidateConfig/Observe guards the obvious offenders (sub-agent tools
	// that load conversation history); new *_execute tools that add
	// conversation-scoped telemetry should be audited before becoming
	// reachable here. Tracked as a v2 hardening: thread a WatchID through
	// NbToolContext so downstream save paths can opt-out on it.
	toolCtx := toolcore.NewNbToolContext(
		ctx, tool,
		w.AccountID.String(),
		userIDString(w.UserID),
		w.ID.String(), // ConversationID — synthetic uuid, unique per watch
		w.ID.String(), // MessageId — same uuid; will not match any real row
		"",            // parent agent id
		"",            // query: not used by single-call tool poll
		nil,
		"",          // queryContext
		queryConfig, // honors tool_config_name / first-enabled fallback
		"",          // toolCallId
	)

	// Fall back: if the agent stored the command inside tool_input.command
	// (a common shape for shell-style tools), surface it as Command so the
	// tool's Call sees it. Without this, github_execute / kubectl_execute
	// receive an empty command string and silently produce no output.
	command := cfg.Command
	if command == "" {
		if v, ok := cfg.ToolInput["command"].(string); ok {
			command = v
		}
	}

	resp, err := tool.Call(toolCtx, toolcore.NBToolCallRequest{
		Command:   command,
		Arguments: cfg.ToolInput,
	})
	if err != nil {
		return Observation{}, fmt.Errorf("tool source: tool %q invocation failed: %w", cfg.ToolName, err)
	}
	if resp.Status == toolcore.NBToolResponseStatusError {
		return Observation{}, fmt.Errorf("tool source: tool %q returned error: %s", cfg.ToolName, truncate(resp.Data, 500))
	}
	return Observation{Data: resp.Data}, nil
}

// ---------------------------------------------------------------------------
// sql source
// ---------------------------------------------------------------------------

// sqlSourceConfig is the JSON shape stored in source_config when
// source_kind = "sql". v1 only allows queries against the metastore; this
// is intentional — exposing arbitrary connection strings to agent-authored
// configs would be a credential exfiltration risk. Cross-datasource SQL is
// a deliberate follow-up.
type sqlSourceConfig struct {
	Datasource string `json:"datasource"`
	Query      string `json:"query"`
	Params     []any  `json:"params,omitempty"`
	// MaxRows caps the number of rows aggregated into the observation
	// payload. Defaults to 100; predicates run against the JSON-rendered
	// rows so wide result sets bloat the LLM judge prompt.
	MaxRows int `json:"max_rows,omitempty"`
}

const (
	sqlSourceDefaultMaxRows = 100
	sqlSourceMaxRowsCeiling = 1000
	sqlSourceQueryTimeout   = 15 * time.Second
)

type sqlSource struct{}

func (sqlSource) Kind() SourceKind { return SourceKindSQL }

func (sqlSource) ValidateConfig(rawCfg json.RawMessage) error {
	// sqlSource has no per-tenant row filter — a SELECT against a shared
	// metastore table sees rows from every tenant. Until the source runs
	// under a tenant-scoped DB role or Postgres RLS, gate the entire kind
	// behind an explicit ops flag so user-driven watches can't reach it
	// when only WatchEnabled is set. Internal smoke tests / dev can flip
	// LLM_SERVER_WATCH_SQL_SOURCE_ENABLED=true to use it.
	if !config.Config.WatchSqlSourceEnabled {
		return fmt.Errorf("sql source: disabled (LLM_SERVER_WATCH_SQL_SOURCE_ENABLED=false); cross-tenant scoping is not enforced for sql sources")
	}
	var cfg sqlSourceConfig
	if err := json.Unmarshal(rawCfg, &cfg); err != nil {
		return fmt.Errorf("sql source: invalid config json: %w", err)
	}
	if cfg.Datasource == "" {
		cfg.Datasource = "metastore"
	}
	if cfg.Datasource != "metastore" {
		return fmt.Errorf("sql source: datasource %q not allowed; v1 supports only \"metastore\"", cfg.Datasource)
	}
	if cfg.Query == "" {
		return fmt.Errorf("sql source: query is required")
	}
	// Reject obviously-mutating SQL even though the metastore connection
	// is shared with the rest of the service. This is belt-and-suspenders;
	// downstream code paths should not be relying on this for security.
	if hasMutatingPrefix(cfg.Query) {
		return fmt.Errorf("sql source: only SELECT/WITH queries are allowed")
	}
	return nil
}

func (sqlSource) Observe(ctx *security.RequestContext, w Watch) (Observation, error) {
	// Re-check the kill switch at observe time, not just create time. Flipping
	// LLM_SERVER_WATCH_SQL_SOURCE_ENABLED=false must immediately stop polling
	// of watches that were registered while it was on — otherwise an
	// already-registered SQL watch keeps running agent-authored SQL against the
	// shared metastore after the operator disabled the feature.
	if !config.Config.WatchSqlSourceEnabled {
		return Observation{}, fmt.Errorf("sql source: disabled (LLM_SERVER_WATCH_SQL_SOURCE_ENABLED=false)")
	}
	var cfg sqlSourceConfig
	if err := json.Unmarshal([]byte(w.SourceConfig), &cfg); err != nil {
		return Observation{}, fmt.Errorf("sql source: corrupted config: %w", err)
	}
	if cfg.MaxRows <= 0 {
		cfg.MaxRows = sqlSourceDefaultMaxRows
	}
	if cfg.MaxRows > sqlSourceMaxRowsCeiling {
		cfg.MaxRows = sqlSourceMaxRowsCeiling
	}

	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		return Observation{}, fmt.Errorf("sql source: metastore unavailable: %w", err)
	}

	queryCtx, cancel := context.WithTimeout(ctx.GetContext(), sqlSourceQueryTimeout)
	defer cancel()

	// Run inside a Postgres READ ONLY transaction. The hasMutatingPrefix
	// allowlist accepts WITH ..., but Postgres CTEs can contain
	// DELETE/UPDATE/INSERT (`WITH x AS (DELETE ... RETURNING *) SELECT ...`)
	// which would slip past prefix-only checks. A READ ONLY transaction
	// rejects all DML at the engine level regardless of where it appears.
	tx, err := dbms.Db.BeginTxx(queryCtx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Observation{}, fmt.Errorf("sql source: read-only tx begin failed: %w", err)
	}
	// Always rollback (no writes possible anyway); commit would also be fine.
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
			ctx.GetLogger().Warn("watch.sql: rollback failed", "error", rerr)
		}
	}()

	// Belt for the suspenders: queryCtx cancels the Go-side wait, but
	// without a server-side statement_timeout Postgres keeps executing a
	// runaway agent-authored SELECT until it finishes — wasting CPU and
	// connections on the shared metastore. SET LOCAL bounds this tx only.
	if _, err := tx.ExecContext(queryCtx, "SET LOCAL statement_timeout = '15s'"); err != nil {
		return Observation{}, fmt.Errorf("sql source: set statement_timeout: %w", err)
	}

	rows, err := tx.QueryxContext(queryCtx, cfg.Query, cfg.Params...)
	if err != nil {
		return Observation{}, fmt.Errorf("sql source: query failed: %w", err)
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil {
			ctx.GetLogger().Warn("watch.sql: rows.Close failed", "error", cerr)
		}
	}()

	collected := make([]map[string]any, 0, cfg.MaxRows)
	truncated := false
	for rows.Next() {
		if len(collected) >= cfg.MaxRows {
			truncated = true
			break
		}
		row := map[string]any{}
		if err := rows.MapScan(row); err != nil {
			return Observation{}, fmt.Errorf("sql source: row scan failed: %w", err)
		}
		// []byte values from the driver are not JSON-friendly — render as
		// strings so the predicate sees a stable shape.
		for k, v := range row {
			if b, ok := v.([]byte); ok {
				row[k] = string(b)
			}
		}
		collected = append(collected, row)
	}
	if err := rows.Err(); err != nil {
		return Observation{}, fmt.Errorf("sql source: row iteration failed: %w", err)
	}

	out := map[string]any{
		"row_count": len(collected),
		"rows":      collected,
	}
	if truncated {
		out["truncated"] = true
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		return Observation{}, fmt.Errorf("sql source: marshal failed: %w", err)
	}
	return Observation{Data: string(encoded), Truncated: truncated}, nil
}

// hasMutatingPrefix returns true unless the query begins with SELECT or WITH
// (case-insensitive, ignoring leading whitespace and a single opening paren).
// Belt-and-suspenders against agents authoring DML; not a substitute for
// running watches under a least-privileged DB role.
func hasMutatingPrefix(q string) bool {
	trimmed := strings.TrimLeft(q, " \t\n\r(")
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "SELECT") {
		return false
	}
	if strings.HasPrefix(upper, "WITH ") || strings.HasPrefix(upper, "WITH\t") || strings.HasPrefix(upper, "WITH\n") {
		return false
	}
	return true
}

func userIDString(u *uuid.UUID) string {
	if u == nil {
		return ""
	}
	return u.String()
}

// pickCommandFromConfig returns the most likely "command string" the
// configured source will actually execute, for the purpose of pre-flight
// inspection. Honours `Command` first, then `tool_input.command` (the
// shell-style shape every *_execute tool routes on), else "" if neither
// is present (some custom tools take a typed args map).
func pickCommandFromConfig(cfg *toolSourceConfig) string {
	if strings.TrimSpace(cfg.Command) != "" {
		return cfg.Command
	}
	if v, ok := cfg.ToolInput["command"].(string); ok {
		return v
	}
	return ""
}

// mutatingVerbPatterns is the deny-list of common destructive command
// prefixes the validator rejects. Intentionally conservative — anchored
// at the start of the command (after leading whitespace), case-insensitive,
// targeting the obvious mistakes a planner LLM might make. Not a sandbox:
// a determined model can obfuscate past any regex (subshells, piped tools,
// aliases), and we don't try to enumerate every destructive verb of every
// CLI. Pair this with the prompt-side guidance ("idempotent, read-only").
var mutatingVerbPatterns = []*regexp.Regexp{
	// kubectl mutating subcommands. `rollout status` and `rollout history`
	// are read-only; only restart/undo/pause/resume mutate.
	regexp.MustCompile(`(?i)^\s*kubectl\s+(delete|apply|create|replace|patch|edit|scale|set|annotate|label|taint|cordon|uncordon|drain|exec)\b`),
	regexp.MustCompile(`(?i)^\s*kubectl\s+rollout\s+(restart|undo|pause|resume)\b`),
	// helm mutating operations.
	regexp.MustCompile(`(?i)^\s*helm\s+(install|upgrade|uninstall|rollback|delete)\b`),
	// File-system destructive.
	regexp.MustCompile(`(?i)^\s*rm\s+-[a-z]*r[fF]?[a-z]*\b`),
	regexp.MustCompile(`(?i)^\s*mv\b`),
	regexp.MustCompile(`(?i)^\s*dd\b`),
	regexp.MustCompile(`(?i)^\s*mkfs\b`),
	// GitHub CLI write paths. `gh run view`, `gh release view`, `gh repo view`
	// are read; deletes / rerun-failed / disable are writes.
	regexp.MustCompile(`(?i)^\s*gh\s+(release|repo|secret|variable|workflow|run|api)\s+(delete|disable|cancel|rerun|set|create|edit|deploy)\b`),
	regexp.MustCompile(`(?i)^\s*gh\s+(pr|issue)\s+(close|merge|reopen|delete|edit|comment|create)\b`),
	// AWS CLI write paths. Anything whose verb starts with `delete-`,
	// `terminate-`, `create-`, `put-`, `update-`, `modify-`, `stop-`,
	// `start-`, `reboot-`, `attach-`, `detach-`, `tag-`, `untag-`. The
	// read verbs are `describe-`, `list-`, `get-`, `search-` which we
	// deliberately do NOT match.
	regexp.MustCompile(`(?i)^\s*aws\s+\S+\s+(delete-|terminate-|create-|put-|update-|modify-|stop-|start-|reboot-|attach-|detach-|tag-|untag-)`),
	// Terraform / IaC.
	regexp.MustCompile(`(?i)^\s*terraform\s+(apply|destroy|taint|untaint|import|state\s+(rm|mv|replace))\b`),
	// argocd / flux mutating ops.
	regexp.MustCompile(`(?i)^\s*argocd\s+(app|repo|cluster)\s+(sync|delete|create|set|patch|rollback)\b`),
	regexp.MustCompile(`(?i)^\s*flux\s+(install|uninstall|create|delete|suspend|resume|reconcile)\b`),
}

// looksMutating returns (verb, true) if `cmd` starts with a known
// destructive verb per mutatingVerbPatterns. The returned verb is a short
// human-readable label for the error message, never the user's input
// echoed back (to avoid leaking sensitive arguments).
func looksMutating(cmd string) (string, bool) {
	for _, re := range mutatingVerbPatterns {
		if m := re.FindString(cmd); m != "" {
			return strings.TrimSpace(m), true
		}
	}
	return "", false
}
