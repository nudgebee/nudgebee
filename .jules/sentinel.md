# Sentinel Security Journal

## 2026-05-19 - Command validation bypass via absolute-path shell interpreters
**Vulnerability:** `detectBypassPatterns` in `llm/code-analysis/api/handlers/execution_handler.go` checked for pipe-to-shell patterns using exact string matching (`| sh`, `| bash`). Absolute paths (`| /bin/sh`, `| /usr/bin/bash`) and direct `bash -c` invocations bypassed all validation, allowing encoded payloads to execute arbitrary commands.
**Learning:** Blocklist-based command validation must normalize commands before matching — checking basenames rather than exact strings. Also, shell `-c` is a first-class bypass vector that must be explicitly blocked since it allows payload encoding.
**Prevention:** Any future command validation changes should add bypass test cases alongside the fix and extract basenames from paths before comparing against known shell names.
