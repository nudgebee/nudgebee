package tools

import (
	"encoding/json"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"nudgebee/llm/tools/core"
)

// readOnlyShellCommands only read files or print. In a conversation holding a
// synced skill's scripts, a command runs without approval only when every
// command in it is one of these or a CLI that has its own read/write
// classifier (kubectl, aws, …). Everything else (interpreters, shells, source,
// eval, wrappers like timeout or env, awk, xargs, any path) needs approval: an
// allow list, so a spelling we did not foresee asks instead of running. A
// non-nil rule reports whether the arguments make the command run a program
// or write a file (find -exec, rg --pre, sort --compress-program, sed e/w).
var readOnlyShellCommands = map[string]func(args []string) bool{
	"cat": nil, "head": nil, "tail": nil, "ls": nil, "tree": nil, "grep": nil, "egrep": nil, "fgrep": nil,
	"wc": nil, "uniq": nil, "cut": nil, "tr": nil, "nl": nil, "column": nil, "diff": nil, "cmp": nil,
	"jq": nil, "yq": nil, "echo": nil, "printf": nil, "pwd": nil, "cd": nil, "stat": nil, "file": nil,
	"du": nil, "df": nil, "basename": nil, "dirname": nil, "realpath": nil, "readlink": nil, "date": nil,
	"true": nil, "sha256sum": nil, "sha1sum": nil, "md5sum": nil, "base64": nil, "which": nil,
	"find": func(args []string) bool { return anyArg(args, func(a string) bool { return findRunsCommand[a] }) },
	// rg --pre runs a program on every file it searches.
	"rg": func(args []string) bool {
		return anyArg(args, func(a string) bool { return strings.HasPrefix(a, "--pre") })
	},
	// sort --compress-program runs a program; GNU accepts any unambiguous
	// prefix of it (--co…).
	"sort": func(args []string) bool {
		return anyArg(args, func(a string) bool { return strings.HasPrefix(a, "--co") })
	},
	"sed": func(args []string) bool { return !sedOnlyPrints(args) },
}

// findRunsCommand flags find's actions that run, delete or write things.
var findRunsCommand = map[string]bool{
	"-exec": true, "-execdir": true, "-ok": true, "-okdir": true, "-delete": true,
	"-fprint": true, "-fprint0": true, "-fprintf": true, "-fls": true,
}

func anyArg(args []string, match func(string) bool) bool {
	for _, a := range args {
		if match(a) {
			return true
		}
	}
	return false
}

// sedPrintRange is the one sed script that only reads: print a line or a
// range ("120p", "51,120p"). sed is otherwise not read-only (w, -i, e).
var sedPrintRange = regexp.MustCompile(`^[0-9]+(,[0-9]+)?p$`)

// sedOnlyPrints reports whether sed's (unquoted) arguments are -n, a
// line-range print and file names: the way agents read part of a file.
func sedOnlyPrints(args []string) bool {
	if len(args) < 3 || args[0] != "-n" || !sedPrintRange.MatchString(args[1]) {
		return false
	}
	return !anyArg(args[2:], func(a string) bool { return strings.HasPrefix(a, "-") })
}

// ShellCommandRunsCode reports whether a shell_execute input (JSON with a
// "command", or the bare command) may run code, for a conversation holding a
// synced skill's scripts. The command is parsed as bash and it is fail-closed:
// it returns false only when every statement is a plain call (or a pipe or
// &&/|| of them) to a read-only command or a classified CLI, every word is a
// literal (no $, `, $( ), <( ), arithmetic), and nothing is redirected into a
// file but /dev/null. Anything else — a parse error, a subshell, if/for,
// a function, VAR=value, a background job — counts as code.
func ShellCommandRunsCode(input string) bool {
	command := strings.TrimSpace(extractCommandFromToolInput(input))
	if command == "" {
		return false
	}
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return true
	}
	for _, stmt := range file.Stmts {
		if stmtRunsCode(stmt) {
			return true
		}
	}
	return false
}

func stmtRunsCode(s *syntax.Stmt) bool {
	if s.Background || s.Coprocess {
		return true
	}
	for _, r := range s.Redirs {
		if redirectRunsCode(r) {
			return true
		}
	}
	switch c := s.Cmd.(type) {
	case nil:
		return false // only redirects, checked above
	case *syntax.CallExpr:
		return callRunsCode(c)
	case *syntax.BinaryCmd: // &&, ||, |, |&
		return stmtRunsCode(c.X) || stmtRunsCode(c.Y)
	default: // subshell, block, if/for/while/case, function, declare, [[ ]], let, time
		return true
	}
}

func callRunsCode(c *syntax.CallExpr) bool {
	if len(c.Assigns) > 0 || len(c.Args) == 0 {
		return true // VAR=value [command]
	}
	args := make([]string, len(c.Args))
	for i, w := range c.Args {
		v, ok := literalWord(w)
		if !ok {
			return true
		}
		args[i] = v
	}
	name := args[0]
	if strings.Contains(name, "/") {
		return true // a path run directly
	}
	if rule, ok := readOnlyShellCommands[name]; ok {
		return rule != nil && rule(args[1:])
	}
	return core.LookupShellWrappable(name) == ""
}

// redirectRunsCode allows reads (<, here-docs with a literal body) and output
// to /dev/null or another descriptor; writing a file counts, so a skill's
// script can't be changed without a card.
func redirectRunsCode(r *syntax.Redirect) bool {
	target, ok := "", true
	if r.Word != nil {
		target, ok = literalWord(r.Word)
	}
	if !ok {
		return true
	}
	switch r.Op {
	case syntax.RdrIn, syntax.WordHdoc:
		return false
	case syntax.Hdoc, syntax.DashHdoc:
		if r.Hdoc == nil {
			return false
		}
		_, literal := literalWord(r.Hdoc)
		return !literal // an unquoted here-doc expands $( ) in its body
	case syntax.RdrOut, syntax.AppOut, syntax.ClbOut, syntax.RdrAll, syntax.AppAll:
		return target != "/dev/null"
	case syntax.DplIn, syntax.DplOut:
		return strings.Trim(target, "0123456789-") != "" // >&2 or >&- , not >&file
	default:
		return true
	}
}

// literalWord is a word's value when it has no expansion: plain text and
// '…' / "…" quotes around plain text, with quotes and backslashes removed as
// the shell does. Anything that expands ($x, $( ), <( ), $(( )), $'…') is not.
func literalWord(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(unescape(p.Value))
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", false
			}
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			if p.Dollar {
				return "", false
			}
			for _, inner := range p.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(unescape(lit.Value))
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}

func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	escaped := false
	for _, r := range s {
		if !escaped && r == '\\' {
			escaped = true
			continue
		}
		escaped = false
		b.WriteRune(r)
	}
	return b.String()
}

// skillFolderPath matches a path into a skill folder copied to a workspace:
// "skills/<name>" at the start of a word or after a "/" (an absolute path into
// another conversation's folder).
var skillFolderPath = regexp.MustCompile(`(^|[\s'"=:/])skills/[^\s/'"]+`)

// ShellCommandNamesSkillFolder reports whether a shell_execute input's command
// or work_dir points into a skill folder (skills/<name>/...).
func ShellCommandNamesSkillFolder(input string) bool {
	if skillFolderPath.MatchString(extractCommandFromToolInput(input)) {
		return true
	}
	var parsed struct {
		WorkDir string `json:"work_dir"`
	}
	if err := json.Unmarshal([]byte(input), &parsed); err == nil && skillFolderPath.MatchString(parsed.WorkDir) {
		return true
	}
	return false
}

// skillFolderName captures the skill in a skills/<name> path.
var skillFolderName = regexp.MustCompile(`(?:^|[\s'"=:/])skills/([^\s/'"]+)`)

// ConfirmationQuestion words the approval card for a command that may run
// code on a skill folder: "update resources on cluster X" would mislead. It
// doesn't claim the command runs the skill's script: it may write the agent's
// own new skill, or read a file with python3 -c. Any other command keeps the
// default card (""). The "Command - " tail is kept for the approval audit.
func (m ShellTool) ConfirmationQuestion(toolInput string) string {
	if !ShellCommandRunsCode(toolInput) {
		return ""
	}
	name, workDir := skillFolderOf(toolInput)
	if name == "" {
		return ""
	}
	return "Run this command on skill folder **" + name + "** in the workspace" + inWorkDir(workDir) +
		"? Skill files may come from a repository rather than from Nudgebee, so each command that can run code there needs approval.\nCommand - " +
		strings.TrimSpace(extractCommandFromToolInput(toolInput))
}

// SkillConversationQuestion words the approval card for a command that may
// run code in a conversation holding a skill's scripts, when the command
// names no skill folder (ConfirmationQuestion covers those).
func SkillConversationQuestion(toolInput string) string {
	_, workDir := skillFolderOf(toolInput)
	return "Run this command in the workspace" + inWorkDir(workDir) +
		"? This conversation has skill files from a repository rather than from Nudgebee, so each command that can run code needs approval.\nCommand - " +
		strings.TrimSpace(extractCommandFromToolInput(toolInput))
}

// skillFolderOf is the skill folder a shell_execute input names (in work_dir
// or the command), and its work_dir.
func skillFolderOf(toolInput string) (name, workDir string) {
	var parsed struct {
		WorkDir string `json:"work_dir"`
	}
	_ = json.Unmarshal([]byte(toolInput), &parsed)
	for _, s := range []string{parsed.WorkDir, extractCommandFromToolInput(toolInput)} {
		if m := skillFolderName.FindStringSubmatch(s); m != nil {
			return m[1], parsed.WorkDir
		}
	}
	return "", parsed.WorkDir
}

func inWorkDir(workDir string) string {
	if workDir == "" {
		return ""
	}
	return " (in " + workDir + ")"
}
