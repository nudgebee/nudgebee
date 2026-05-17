# Sentinel Security Journal

## 2026-05-17 - Command Injection via Shell Interpolation in PR Followup Agent

**Vulnerability:** `pr_followup_agent.go` constructed shell command strings by interpolating external data (repo paths, branch names, PR numbers) into `fmt.Sprintf` templates, then executed them via `exec.Command("sh", "-c", cmd)`. This allowed shell metacharacter injection through any of these fields.

**Learning:** The safe `runCommandInDir(name, args...)` helper already called `exec.Command` directly — but callers bypassed its safety by funneling everything through `sh -c`. The `providerJQQuery` function showed the correct pattern was already known in this codebase but wasn't applied consistently.

**Prevention:** Never use `sh -c` with interpolated strings. Pass arguments as separate parameters to exec.Command. Grep for `"sh", "-c"` in code reviews.
