package agents

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"nudgebee/code-analysis-agent/common"
	"nudgebee/code-analysis-agent/internal/session"
	"nudgebee/code-analysis-agent/tools"
)

// VerificationStatus is the honest tri-state outcome of harness-run verification.
// There is deliberately no way to express "accepted anyway": a fix is verified,
// failed with evidence, or explicitly unverified with the reason why.
type VerificationStatus string

const (
	VerificationVerified   VerificationStatus = "verified"
	VerificationFailed     VerificationStatus = "failed"
	VerificationUnverified VerificationStatus = "unverified"
)

// VerificationStep is one command the harness ran (or skipped), with its real
// exit codes and bounded output. Output/SpillPath come from the CLI tool, so
// per-stage pipeline honesty and spill-don't-drop apply here too.
type VerificationStep struct {
	Dir            string `json:"dir"`
	Command        string `json:"command"`
	ExitCode       int    `json:"exit_code"`
	StageExitCodes []int  `json:"stage_exit_codes,omitempty"`
	Output         string `json:"output,omitempty"`
	SpillPath      string `json:"spill_path,omitempty"`
	Skipped        bool   `json:"skipped,omitempty"`
	SkipReason     string `json:"skip_reason,omitempty"`
	Passed         bool   `json:"passed"`
}

// VerificationResult is the deterministic verdict the orchestrator attaches to a
// fix. It replaces both the LLM's self-reported verification and the old
// pattern-matched reconstruction of the fixer's command history.
type VerificationResult struct {
	Status VerificationStatus `json:"status"`
	Steps  []VerificationStep `json:"steps,omitempty"`
	Reason string             `json:"reason,omitempty"`
}

// Evidence renders the verbatim (bounded) output of every step — this is what
// feedback, logs, and PR bodies embed. Never empty when something ran.
func (r VerificationResult) Evidence() string {
	if len(r.Steps) == 0 {
		if r.Reason != "" {
			return "not verified: " + r.Reason
		}
		return "not verified"
	}
	var b strings.Builder
	for _, s := range r.Steps {
		if s.Skipped {
			fmt.Fprintf(&b, "SKIPPED (cd %s && %s): %s\n", s.Dir, s.Command, s.SkipReason)
			continue
		}
		verdict := "PASSED"
		if !s.Passed {
			verdict = "FAILED"
		}
		fmt.Fprintf(&b, "%s (cd %s && %s) exit=%d", verdict, s.Dir, s.Command, s.ExitCode)
		if len(s.StageExitCodes) > 1 {
			fmt.Fprintf(&b, " stage_exits=%v", s.StageExitCodes)
		}
		b.WriteString("\n")
		if out := strings.TrimSpace(s.Output); out != "" {
			b.WriteString(out)
			b.WriteString("\n")
		}
		if s.SpillPath != "" {
			fmt.Fprintf(&b, "(full output: %s)\n", s.SpillPath)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ToMap renders the result as a plain map for embedding in agent responses.
func (r VerificationResult) ToMap() map[string]any {
	m := map[string]any{
		"status":   string(r.Status),
		"evidence": r.Evidence(),
	}
	if r.Reason != "" {
		m["reason"] = r.Reason
	}
	if len(r.Steps) > 0 {
		steps := make([]map[string]any, 0, len(r.Steps))
		for _, s := range r.Steps {
			step := map[string]any{
				"dir":     s.Dir,
				"command": s.Command,
				"passed":  s.Passed,
			}
			if s.Skipped {
				step["skipped"] = true
				step["skip_reason"] = s.SkipReason
			} else {
				step["exit_code"] = s.ExitCode
			}
			steps = append(steps, step)
		}
		m["steps"] = steps
	}
	return m
}

// truncateRuneSafe caps s at max bytes without splitting a multi-byte rune,
// appending an ellipsis when anything was cut.
func truncateRuneSafe(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max] + "…"
}

// verificationPRSection renders the verification verdict for a PR body. Returns
// "" when no harness verification result is present (legacy path).
func verificationPRSection(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	status, _ := m["status"].(string)
	evidence, _ := m["evidence"].(string)
	evidence = truncateRuneSafe(evidence, 1500)
	switch VerificationStatus(status) {
	case VerificationVerified:
		s := "\n\n---\n\n### ✅ Verification\n\nBuild verification passed for the affected modules.\n"
		if evidence != "" {
			s += "\n<details><summary>Commands run</summary>\n\n```\n" + evidence + "\n```\n\n</details>\n"
		}
		return s
	case VerificationUnverified:
		reason, _ := m["reason"].(string)
		s := "\n\n---\n\n### ⚠️ Verification\n\nThis change was **not** verified by an automated build"
		if reason != "" {
			s += " (" + reason + ")"
		}
		return s + ". Review accordingly.\n"
	case VerificationFailed:
		return "\n\n---\n\n### ❌ Verification\n\nBuild verification **failed**:\n\n```\n" + evidence + "\n```\n"
	}
	return ""
}

// FixVerifier runs deterministic, harness-owned build verification for a fix.
// The command is never guessed: an explicit request BuildConfig wins; otherwise
// module manifests discovered by the repo index scope the manifest's standard
// build command to the modules the fix actually touched; otherwise the result is
// honestly "unverified". The LLM interprets this result — it does not produce it.
type FixVerifier struct {
	logger  *common.Logger
	timeout time.Duration
}

func NewFixVerifier(logger *common.Logger, timeout time.Duration) *FixVerifier {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	return &FixVerifier{logger: logger, timeout: timeout}
}

// Verify builds the modules affected by the working-tree changes in workspaceDir.
func (v *FixVerifier) Verify(ctx context.Context, workspaceDir string, buildCfg *session.BuildConfig) VerificationResult {
	if buildCfg != nil {
		if steps := buildConfigCommands(buildCfg); len(steps) > 0 {
			return v.runSteps(ctx, workspaceDir, ".", steps)
		}
	}

	changed, err := v.changedFiles(ctx, workspaceDir)
	if err != nil {
		return VerificationResult{Status: VerificationUnverified, Reason: fmt.Sprintf("could not determine changed files: %v", err)}
	}
	if len(changed) == 0 {
		return VerificationResult{Status: VerificationUnverified, Reason: "no changed files detected in the workspace"}
	}

	idx, err := tools.IndexRepository(workspaceDir)
	if err != nil || idx == nil || len(idx.ModuleRoots) == 0 {
		return VerificationResult{Status: VerificationUnverified, Reason: "no build modules discovered in the repository"}
	}

	modules := modulesForFiles(idx.ModuleRoots, changed)
	if len(modules) == 0 {
		return VerificationResult{
			Status: VerificationUnverified,
			Reason: fmt.Sprintf("changed files (%s) are not inside any discovered build module", strings.Join(changed, ", ")),
		}
	}

	result := VerificationResult{Status: VerificationVerified}
	ranAny := false
	for _, m := range modules {
		if reason := preflightSkip(workspaceDir, m); reason != "" {
			result.Steps = append(result.Steps, VerificationStep{Dir: m.Path, Command: m.Build, Skipped: true, SkipReason: reason})
			continue
		}
		step := v.runCommand(ctx, workspaceDir, m.Path, m.Build)
		result.Steps = append(result.Steps, step)
		ranAny = true
		if !step.Passed {
			result.Status = VerificationFailed
		}
	}
	if !ranAny && result.Status == VerificationVerified {
		result.Status = VerificationUnverified
		result.Reason = "all affected modules were skipped (toolchain or dependencies unavailable in this environment)"
	}
	return result
}

// buildConfigCommands flattens an explicit BuildConfig into ordered commands.
func buildConfigCommands(cfg *session.BuildConfig) []string {
	var cmds []string
	for _, c := range []string{cfg.SetupCommand, cfg.LintCommand, cfg.BuildCommand, cfg.TestCommand} {
		if strings.TrimSpace(c) != "" {
			cmds = append(cmds, c)
		}
	}
	return cmds
}

func (v *FixVerifier) runSteps(ctx context.Context, workspaceDir, dir string, commands []string) VerificationResult {
	result := VerificationResult{Status: VerificationVerified}
	for _, cmd := range commands {
		step := v.runCommand(ctx, workspaceDir, dir, cmd)
		result.Steps = append(result.Steps, step)
		if !step.Passed {
			result.Status = VerificationFailed
			break // later steps would report noise caused by the first failure
		}
	}
	return result
}

// runCommand executes one verification command through the CLI tool (untracked —
// this is harness work, not an LLM action) so per-stage exit honesty, output
// bounding, and spill files all apply.
func (v *FixVerifier) runCommand(ctx context.Context, workspaceDir, dir, command string) VerificationStep {
	step := VerificationStep{Dir: dir, Command: command}
	cli := tools.NewCLITool(workspaceDir)
	input := map[string]any{
		"command": command,
		"timeout": int(v.timeout.Seconds()),
	}
	if dir != "" && dir != "." {
		input["working_dir"] = dir
	}
	if v.logger != nil {
		v.logger.Log(common.EventStepStart, "Harness verification command", map[string]any{"dir": dir, "command": command})
	}
	resp := cli.Execute(ctx, input)
	step.Passed = resp.Status != "error"
	if data, ok := resp.Data.(map[string]any); ok {
		if out, ok := data["result"].(*tools.CLIOutput); ok {
			step.ExitCode = out.ExitCode
			step.StageExitCodes = out.StageExitCodes
			step.SpillPath = out.SpillPath
			step.Output = strings.TrimSpace(strings.TrimSpace(out.Stdout) + "\n" + strings.TrimSpace(out.Stderr))
		}
	}
	if step.Output == "" && resp.Error != "" {
		step.Output = resp.Error
	}
	if v.logger != nil {
		v.logger.Log(common.EventStepComplete, "Harness verification command finished", map[string]any{
			"dir": dir, "command": command, "passed": step.Passed, "exit_code": step.ExitCode,
		})
	}
	return step
}

// changedFiles lists working-tree changes (modified + untracked) via git.
func (v *FixVerifier) changedFiles(ctx context.Context, workspaceDir string) ([]string, error) {
	cli := tools.NewCLITool(workspaceDir)
	resp := cli.Execute(ctx, map[string]any{"command": "git status --porcelain", "timeout": 60})
	if resp.Status == "error" {
		return nil, fmt.Errorf("git status failed: %s", resp.Error)
	}
	var out string
	if data, ok := resp.Data.(map[string]any); ok {
		if r, ok := data["result"].(*tools.CLIOutput); ok {
			out = r.Stdout
		}
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		// Renames are reported as "old -> new"; the new path is what got built.
		if idx := strings.Index(path, " -> "); idx != -1 {
			path = path[idx+4:]
		}
		path = strings.Trim(path, `"`)
		if path != "" {
			files = append(files, path)
		}
	}
	return files, nil
}

// modulesForFiles maps each changed file to the deepest module root containing it
// and returns the unique affected modules in stable order.
func modulesForFiles(roots []tools.ModuleRoot, files []string) []tools.ModuleRoot {
	selected := make(map[string]tools.ModuleRoot)
	for _, f := range files {
		f = filepath.ToSlash(f)
		bestLen := -1
		var best tools.ModuleRoot
		for _, m := range roots {
			p := filepath.ToSlash(m.Path)
			switch {
			case p == "." || p == "":
				if bestLen < 0 {
					bestLen = 0
					best = m
				}
			case f == p || strings.HasPrefix(f, p+"/"):
				if len(p) > bestLen {
					bestLen = len(p)
					best = m
				}
			}
		}
		if bestLen >= 0 {
			selected[best.Path] = best
		}
	}
	modules := make([]tools.ModuleRoot, 0, len(selected))
	for _, m := range selected {
		modules = append(modules, m)
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Path < modules[j].Path })
	return modules
}

// preflightSkip reports why a module cannot be meaningfully verified in this
// environment (missing toolchain, uninstalled dependencies) — a skip, not a
// failure. Verification must never mint environment problems as code failures.
func preflightSkip(workspaceDir string, m tools.ModuleRoot) string {
	fields := strings.Fields(m.Build)
	// Skip leading env-var assignments (`GOOS=linux go build ...`) — the binary
	// to probe is the first non-assignment word.
	cmdIdx := 0
	for cmdIdx < len(fields) && strings.Contains(fields[cmdIdx], "=") {
		cmdIdx++
	}
	if cmdIdx >= len(fields) {
		return "no build command for module"
	}
	if _, err := exec.LookPath(fields[cmdIdx]); err != nil {
		return fmt.Sprintf("%q not available in this environment", fields[cmdIdx])
	}
	if m.Kind == "node" {
		if _, err := os.Stat(filepath.Join(workspaceDir, m.Path, "node_modules")); err != nil {
			return "node dependencies not installed (node_modules missing)"
		}
	}
	return ""
}
