package tools

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"nudgebee/llm/tools/core"

	"github.com/google/shlex"
)

// staticCLIArgs returns shell-parsed arguments for a single CLI invocation.
// Compound commands deliberately fall through to the existing LLM classifier:
// classifying only the first command could approve a later mutation hidden
// behind a separator, substitution, or pipeline.
func staticCLIArgs(input string, allowBare bool, executables ...string) ([]string, bool) {
	command := strings.TrimSpace(extractCommandFromToolInput(input))
	if command == "" || hasUnquotedShellSyntax(command) {
		return nil, false
	}
	tokens, err := shlex.Split(command)
	if err != nil || len(tokens) == 0 {
		return nil, false
	}
	matchedExecutable := false
	for _, executable := range executables {
		if strings.EqualFold(filepath.Base(tokens[0]), executable) {
			tokens = tokens[1:]
			matchedExecutable = true
			break
		}
	}
	if (!matchedExecutable && !allowBare) || len(tokens) == 0 {
		return nil, false
	}
	return tokens, true
}

// expandEscapedNewlines turns a literal `\n` into a newline, as the gh and glab
// tools do before running a command. Their classifiers must read the command
// in that form: a newline outside quotes starts a second shell command.
func expandEscapedNewlines(command string) string {
	return strings.ReplaceAll(command, "\\n", "\n")
}

func helpOrVersion(tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	if isHelpRequest(tokens) {
		return true
	}
	return isHelpOrVersionCommand(tokens)
}

// isHelpOrVersionCommand reports `<cli> help ...` / `<cli> version ...`.
func isHelpOrVersionCommand(tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	first := strings.ToLower(tokens[0])
	return first == "help" || first == "version" ||
		(len(tokens) == 1 && (first == "-v" || first == "--version"))
}

// isHelpRequest reports a command that only asks for help: the help flag is
// the last token and no flag comes before it, as in `<cli> [command words]
// --help`. A help flag anywhere else proves nothing. A flag before it can take
// it as its value (`helm uninstall rel --description -h` uninstalls), and a
// shell comment or another command can hide it from what actually runs.
func isHelpRequest(tokens []string) bool {
	if len(tokens) == 0 {
		return false
	}
	last := tokens[len(tokens)-1] // case-sensitive: `-H` is not `-h`
	if last == "--version" && len(tokens) == 1 {
		return true
	}
	if last != "-h" && last != "--help" {
		return false
	}
	for _, token := range tokens[:len(tokens)-1] {
		if strings.HasPrefix(token, "-") {
			return false
		}
	}
	return true
}

func classifyAction(action string, reads, creates, updates, deletes map[string]struct{}) core.ToolRequestType {
	action = strings.ToLower(action)
	if _, ok := reads[action]; ok {
		return core.ToolRequestTypeRead
	}
	if _, ok := creates[action]; ok {
		return core.ToolRequestTypeCreate
	}
	if _, ok := updates[action]; ok {
		return core.ToolRequestTypeUpdate
	}
	if _, ok := deletes[action]; ok {
		return core.ToolRequestTypeDelete
	}
	return ""
}

func wordSet(words ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(words))
	for _, word := range words {
		result[word] = struct{}{}
	}
	return result
}

var (
	helmReadActions   = wordSet("env", "get", "history", "inspect", "lint", "list", "ls", "search", "show", "status", "template", "verify")
	helmCreateActions = wordSet("install")
	helmUpdateActions = wordSet("rollback", "upgrade")
	helmDeleteActions = wordSet("uninstall")
	helmRepoReads     = wordSet("list")
	helmRepoCreates   = wordSet("add")
	helmRepoUpdates   = wordSet("update")
	helmRepoDeletes   = wordSet("remove")

	githubReadActions   = wordSet("checks", "clone", "diff", "download", "list", "status", "view")
	githubCreateActions = wordSet("create", "fork")
	githubUpdateActions = wordSet("cancel", "close", "comment", "disable", "edit", "enable", "merge", "reopen", "rerun", "run", "set")
	githubDeleteActions = wordSet("delete")

	gitlabReadActions   = wordSet("clone", "list", "status", "trace", "view")
	gitlabCreateActions = wordSet("create", "fork")
	gitlabUpdateActions = wordSet("cancel", "close", "merge", "note", "reopen", "retry", "run", "set", "update")
	gitlabDeleteActions = wordSet("delete")

	argoReadActions   = wordSet("diff", "get", "history", "list")
	argoCreateActions = wordSet("add", "create")
	argoUpdateActions = wordSet("patch", "rollback", "set", "sync", "unset")
	argoDeleteActions = wordSet("delete", "rm", "terminate-op")

	redisReadActions   = wordSet("command", "dbsize", "exists", "get", "hget", "hgetall", "hlen", "hmget", "info", "llen", "lrange", "mget", "ping", "pttl", "scan", "scard", "sismember", "smembers", "ttl", "type", "zrange", "zscore")
	redisCreateActions = wordSet("hset", "lpush", "mset", "rpush", "sadd", "set", "setnx")
	redisUpdateActions = wordSet("append", "decr", "decrby", "expire", "incr", "incrby", "lset", "persist", "pexpire", "rename", "trim", "zadd")
	redisDeleteActions = wordSet("del", "flushall", "flushdb", "hdel", "lpop", "rpop", "srem", "unlink", "zrem")

	rabbitReadActions   = wordSet("get", "list", "overview", "show")
	rabbitCreateActions = wordSet("declare", "publish")
	rabbitUpdateActions = wordSet("purge", "set_permission")
	rabbitDeleteActions = wordSet("close_connection", "delete")
)

func inferHelmRequestType(input string) core.ToolRequestType {
	tokens, ok := staticCLIArgs(input, false, "helm")
	if !ok {
		return ""
	}
	if helpOrVersion(tokens) {
		return core.ToolRequestTypeRead
	}
	if strings.EqualFold(tokens[0], "repo") {
		if len(tokens) < 2 {
			return ""
		}
		return classifyAction(tokens[1], helmRepoReads, helmRepoCreates, helmRepoUpdates, helmRepoDeletes)
	}
	return classifyAction(tokens[0], helmReadActions, helmCreateActions, helmUpdateActions, helmDeleteActions)
}

// githubSearchTypes are the `gh search` subcommands. GitHub search only reads.
var githubSearchTypes = wordSet("code", "commits", "issues", "prs", "repos")

// inferGithubRequestType classifies one gh command: `gh search <type>` is a
// read, everything else goes through the shared nested-CLI rules.
func inferGithubRequestType(command string) core.ToolRequestType {
	if tokens, ok := staticCLIArgs(command, false, "gh"); ok && len(tokens) >= 2 && strings.EqualFold(tokens[0], "search") {
		if _, known := githubSearchTypes[strings.ToLower(tokens[1])]; known {
			return core.ToolRequestTypeRead
		}
		return ""
	}
	return inferNestedCLIRequestType(command, "gh", githubReadActions, githubCreateActions, githubUpdateActions, githubDeleteActions)
}

func inferNestedCLIRequestType(
	input string,
	executable string,
	reads, creates, updates, deletes map[string]struct{},
) core.ToolRequestType {
	tokens, ok := staticCLIArgs(input, false, executable)
	if !ok {
		return ""
	}
	if helpOrVersion(tokens) {
		return core.ToolRequestTypeRead
	}
	if len(tokens) < 2 || strings.HasPrefix(tokens[0], "-") {
		return ""
	}
	if strings.EqualFold(tokens[0], "api") {
		return inferAPIRequestType(tokens[1:])
	}
	return classifyAction(tokens[1], reads, creates, updates, deletes)
}

// inferAPIRequestType classifies the shared `gh api` / `glab api` surface.
// Both default to GET unless a method or input/field flag requests a write.
func inferAPIRequestType(tokens []string) core.ToolRequestType {
	if len(tokens) == 0 {
		return ""
	}
	method := "GET"
	explicitMethod := false
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		key, value, hasValue := strings.Cut(token, "=")
		switch strings.ToLower(key) {
		case "-x", "--method":
			if hasValue {
				if value == "" {
					return ""
				}
				method = strings.ToUpper(value)
			} else {
				if i+1 >= len(tokens) {
					return ""
				}
				i++
				method = strings.ToUpper(tokens[i])
			}
			explicitMethod = true
		case "-f", "--field", "--raw-field", "--form", "--input":
			// gh/glab switch the default method to POST when request data is supplied.
			if !explicitMethod {
				method = "POST"
			}
		}
	}
	return classifyHTTPMethod(method)
}

// redisCommandAsRun returns the shell command the Redis tool runs: the input
// as given when it starts with redis-cli, else the input as redis-cli's
// arguments. The classifier reads this same string. A redis-cli anywhere else
// in the input does not count: "command bash -c 'id' redis-cli" used to run
// as given, so bash ran it, while the classifier read Redis COMMAND.
func redisCommandAsRun(command string) string {
	command = strings.TrimSpace(command)
	if fields := strings.Fields(command); len(fields) > 0 && fields[0] == "redis-cli" {
		return command
	}
	return "redis-cli " + command
}

func inferRedisRequestType(input string) core.ToolRequestType {
	tokens, ok := staticCLIArgs(redisCommandAsRun(extractCommandFromToolInput(input)), false, "redis-cli")
	if !ok {
		return ""
	}
	// Connection flags can take arbitrary values that happen to look like Redis
	// verbs. Defer those forms rather than risk classifying a later mutation by
	// an option value (for example, `-h get SET key value`).
	if strings.HasPrefix(tokens[0], "-") {
		if strings.EqualFold(tokens[0], "--help") || strings.EqualFold(tokens[0], "--version") {
			return core.ToolRequestTypeRead
		}
		return ""
	}
	// Words after a Redis command are its arguments (`DEL key --help` deletes
	// two keys), so only the client's own help command counts.
	if isHelpOrVersionCommand(tokens) {
		return core.ToolRequestTypeRead
	}
	return classifyAction(tokens[0], redisReadActions, redisCreateActions, redisUpdateActions, redisDeleteActions)
}

func inferRabbitRequestType(input string) core.ToolRequestType {
	command := extractRabbitCommand(input)
	tokens, ok := staticCLIArgs(command, false, "rabbitmqadmin", "rabbitmq-api", "rabbitmq")
	if !ok {
		return ""
	}
	// rabbitmq-api passes its trailing words to curl, so a trailing help flag
	// is not proof of a help request; only the help command counts.
	if isHelpOrVersionCommand(tokens) {
		return core.ToolRequestTypeRead
	}
	if strings.HasPrefix(tokens[0], "-") {
		return ""
	}
	commandTokens, err := shlex.Split(command)
	if err == nil && len(commandTokens) > 0 && strings.EqualFold(filepath.Base(commandTokens[0]), "rabbitmq-api") {
		requestType := classifyHTTPMethod(tokens[0])
		// Words after the path go to curl unquoted, after the broker
		// credentials: "-X DELETE" overrides the method (curl uses the last
		// -X) and another URL receives the credentials. Only a bare
		// `rabbitmq-api GET /path` is a read.
		if requestType == core.ToolRequestTypeRead && len(tokens) != 2 {
			return core.ToolRequestTypeUpdate
		}
		return requestType
	}
	requestType := classifyAction(tokens[0], rabbitReadActions, rabbitCreateActions, rabbitUpdateActions, rabbitDeleteActions)
	if requestType == core.ToolRequestTypeRead && strings.EqualFold(tokens[0], "get") && rabbitGetDropsMessages(tokens[1:]) {
		return core.ToolRequestTypeUpdate
	}
	return requestType
}

// rabbitGetDropsMessages reports a rabbitmqadmin `get` that takes messages off
// the queue for good. `get` fetches messages and, by default, puts them back
// (ackmode=ack_requeue_true); any other ack mode, or requeue=false, removes
// them.
func rabbitGetDropsMessages(args []string) bool {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		key, value, hasValue := strings.Cut(strings.ToLower(arg), "=")
		key = strings.TrimLeft(key, "-")
		// `--flag value`: the value is consumed so it is not read again as a
		// setting (`--queue requeue` names a queue). A word that is itself a
		// setting (has `=`) or a flag is never consumed, so it is still checked.
		if !hasValue && strings.HasPrefix(arg, "--") && i+1 < len(args) &&
			!strings.HasPrefix(args[i+1], "-") && !strings.Contains(args[i+1], "=") {
			i++
			value = strings.ToLower(args[i])
		}
		switch key {
		case "ackmode", "ack-mode", "ack_mode":
			if value != "ack_requeue_true" && value != "reject_requeue_true" {
				return true
			}
		case "requeue":
			if value != "true" {
				return true
			}
		}
	}
	return false
}

func extractRabbitCommand(input string) string {
	var command rabbitmqCommand
	if err := json.Unmarshal([]byte(input), &command); err != nil || command.Args == "" {
		return extractCommandFromToolInput(input)
	}
	args := strings.TrimSpace(command.Args)
	if args == "rabbitmq-api" || strings.HasPrefix(args, "rabbitmq-api ") {
		return args
	}
	if command.Command == "" {
		command.Command = "rabbitmqadmin"
	}
	return strings.TrimSpace(command.Command + " " + args)
}

func classifyHTTPMethod(method string) core.ToolRequestType {
	switch strings.ToUpper(method) {
	case "GET", "HEAD", "OPTIONS":
		return core.ToolRequestTypeRead
	case "POST":
		return core.ToolRequestTypeCreate
	case "PUT", "PATCH":
		return core.ToolRequestTypeUpdate
	case "DELETE":
		return core.ToolRequestTypeDelete
	default:
		return ""
	}
}
