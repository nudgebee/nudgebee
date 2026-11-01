package core

import "strings"

// This file is the single, exported source of truth for "does this remote
// command only observe, or can it change something" — used by two callers
// with different stakes:
//   - api/remediation_validate.go scores a generated remediation plan's
//     confidence; a wrong answer there is at worst a misleading score on a
//     suggestion a human still approves before it runs.
//   - tools/tool_kubectl.go and tools/tool_cloud_aws.go use it to decide
//     whether a `kubectl exec` / `aws ssm send-command` in a LIVE, possibly
//     unattended investigation runs WITHOUT ANY human confirmation at all.
//
// The second consumer is why every entry here must be safe under "this runs
// with no one watching," not merely "safe to nudge a suggested fix's score."
// Before adding an entry, ask: can this binary, with some combination of
// flags, mutate remote state or exfiltrate data? If yes, it needs an
// allowlist of its read-only flags (see IsReadOnlyCurlCommand /
// IsReadOnlyWgetCommand / IsReadOnlyNcCommand below) rather than a blind
// first-word entry.

// ReadOnlyRemoteCommands are in-container commands that only report, safe to
// trust by first word alone — none has an ordinary mode that mutates remote
// state or exfiltrates data beyond what the command's own arguments already
// reveal in plain text. `kubectl exec` and `aws ssm send-command` are
// write-capable channels, so they are read-only only when what they run is
// one of these (or passes the flag-aware checks below) — which keeps a
// genuinely useful verify like `kubectl exec zookeeper-0 -- df -h /datalog`
// working without trusting the channel itself.
//
// Deliberately absent: `date` (-s sets the clock), `hostname` (can set the
// name), `top` (interactive, can renice/kill), and `env` — which runs
// whatever follows it, so it is read-only only when bare (see
// remoteCommandIsReadOnly).
var ReadOnlyRemoteCommands = map[string]bool{
	"df": true, "ls": true, "cat": true, "ps": true, "free": true,
	"stat": true, "wc": true, "head": true, "tail": true, "id": true,
	"uptime": true, "pwd": true,
	"grep": true, "du": true,
	// ping has no invocation that mutates remote state or destination
	// service; its only payload-carrying flag (-p <hex>, ICMP echo padding)
	// can't exfiltrate meaningfully more than the destination address the
	// command already states in plain text. Safe as a blanket entry, unlike
	// curl/wget/nc below.
	"ping": true,
}

// ReadOnlySystemctlVerbs are the `systemctl` subcommands that only report.
// `systemctl` is the natural way to verify a service fix on a host, and it
// is the whole command's first word only in the SSM payload form, so it is
// checked separately from ReadOnlyRemoteCommands.
var ReadOnlySystemctlVerbs = map[string]bool{
	"is-active": true, "is-enabled": true, "is-failed": true, "status": true, "show": true,
}

// SSMPayload extracts the shell command inside `aws ssm send-command
// --parameters …`.
//
// The parameter is written several ways by the model — commands=["x"],
// commands="x", 'commands=[x]' — so the payload is taken as whatever follows
// the first `commands=`, stripped of the JSON and quoting scaffolding rather
// than parsed as JSON, which none of these reliably are.
func SSMPayload(command string) string {
	_, after, ok := strings.Cut(command, "commands=")
	if !ok {
		return ""
	}
	after = strings.TrimSpace(after)
	after = strings.Trim(after, `'"`)
	after = strings.TrimPrefix(after, "[")
	if i := strings.IndexAny(after, "]"); i >= 0 {
		after = after[:i]
	}
	after = strings.Trim(after, `'"`)
	// Drop a trailing flag that followed an unquoted payload (`--region us-east-1`).
	if i := strings.Index(after, " --"); i >= 0 {
		after = after[:i]
	}
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(after), `'"`))
}

// SSMPayloadIsReadOnly reports whether an `aws ssm send-command` only
// observes the host.
//
// `send-command` is a write channel, so it is read-only exactly when what it
// runs is — the same reasoning `kubectl exec` gets. Without this, a correct
// verify like `systemctl is-active postgresql` is indistinguishable from a
// genuine mutation and either gets thrown away or, worse, is trusted no more
// carefully than a real remote-command execution.
func SSMPayloadIsReadOnly(command string) bool {
	payload := SSMPayload(command)
	if payload == "" {
		return false
	}
	// A compound payload is not inspectable one word at a time.
	// `"a","b"` survives SSMPayload's scaffolding strip as one string, so the
	// list separator and any leftover quotes also mean "more than one command".
	if strings.ContainsAny(payload, ";&|<>`$\n\"',") {
		return false
	}
	f := strings.Fields(payload)
	if len(f) == 0 {
		return false
	}
	if f[0] == "sudo" {
		f = f[1:]
		if len(f) == 0 {
			return false
		}
	}
	if f[0] == "systemctl" {
		return len(f) > 1 && ReadOnlySystemctlVerbs[f[1]]
	}
	return remoteCommandIsReadOnly(f)
}

// ExecRemoteCommandIsReadOnly inspects the command after the `--` separator
// of a `kubectl exec`.
func ExecRemoteCommandIsReadOnly(fields []string) bool {
	for i, f := range fields {
		if f == "--" && i+1 < len(fields) {
			return remoteCommandIsReadOnly(fields[i+1:])
		}
	}
	return false
}

// remoteCommandIsReadOnly classifies a tokenized remote command (the payload
// after `kubectl exec ... --` or an SSM `commands=` entry) by its first
// word, giving binaries that CAN mutate or exfiltrate (curl, wget, nc, env,
// rabbitmqctl) a check of their arguments instead of a blind allowlist entry.
// Anything not listed asks.
func remoteCommandIsReadOnly(fields []string) bool {
	if len(fields) == 0 {
		return false
	}
	// One simple command only: no chaining, redirection or substitution.
	for _, f := range fields {
		if strings.ContainsAny(f, ";&|<>`$\n") {
			return false
		}
	}
	switch fields[0] {
	case "curl":
		return IsReadOnlyCurlCommand(strings.Join(fields, " "))
	case "wget":
		return IsReadOnlyWgetCommand(fields)
	case "nc":
		return IsReadOnlyNcCommand(fields)
	case "env":
		// `env` execs whatever follows it (`env rm -rf /`); only bare `env`,
		// which prints the environment, is a read.
		return len(fields) == 1
	case "rabbitmqctl":
		return isReadOnlyRabbitmqctl(fields[1:])
	default:
		return ReadOnlyRemoteCommands[fields[0]]
	}
}

// rabbitmqctlFlagsWithValue are the global options that take a separate value,
// skipped when looking for the subcommand.
var rabbitmqctlFlagsWithValue = map[string]bool{
	"-n": true, "--node": true, "-t": true, "--timeout": true,
	"-p": true, "--vhost": true, "-f": true, "--formatter": true,
}

// isReadOnlyRabbitmqctl allows the reporting subcommands: list_*, status,
// *_status, environment and help. Everything else — notably `eval`, which
// runs arbitrary Erlang — asks.
func isReadOnlyRabbitmqctl(args []string) bool {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			if rabbitmqctlFlagsWithValue[a] {
				i++
			}
			continue
		}
		return strings.HasPrefix(a, "list_") || strings.HasSuffix(a, "_status") ||
			a == "status" || a == "environment" || a == "help"
	}
	return false
}

// Remote curl, wget and nc are judged against the flags known to only read,
// and anything else asks. A list of dangerous flags kept missing ones that
// write a file in the pod (curl -D/-c/--trace/--stderr/--libcurl/--etag-save,
// wget -o/--save-cookies) or run a program (nc -e/-c), each of which then ran
// with no approval and no LLM check.
var (
	curlFlags = map[string]bool{
		"-s": true, "--silent": true, "-S": true, "--show-error": true,
		"-i": true, "--include": true, "-I": true, "--head": true,
		"-k": true, "--insecure": true, "-L": true, "--location": true,
		"-v": true, "--verbose": true, "-f": true, "--fail": true,
	}
	curlFlagsWithValue = map[string]bool{
		"-m": true, "--max-time": true, "--connect-timeout": true,
		"-H": true, "--header": true, "-w": true, "--write-out": true,
		"-u": true, "--user": true,
	}
)

const (
	curlShortFlags          = "sSiIkLvf"
	curlShortFlagsWithValue = "mHwu"
)

// IsReadOnlyCurlCommand reports whether a curl invocation uses only flags that
// read: a GET whose response goes to stdout.
func IsReadOnlyCurlCommand(input string) bool {
	tokens := strings.Fields(input)
	if len(tokens) == 0 || tokens[0] != "curl" {
		return false
	}
	args := tokens[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-" {
			return false
		}
		if !strings.HasPrefix(a, "-") {
			continue // URL
		}
		value, hasValue := "", false
		if strings.HasPrefix(a, "--") {
			name, v, assigned := strings.Cut(a, "=")
			switch {
			case curlFlags[name] && !assigned:
				continue
			case !curlFlagsWithValue[name]:
				return false
			case assigned:
				value, hasValue = v, true
			case i+1 < len(args):
				i++
				value, hasValue = args[i], true
			default:
				return false
			}
		} else {
			// Short options, possibly bundled (-sS, -sSm5). A value flag takes
			// the rest of the token, or the next argument.
			for j := 1; j < len(a); j++ {
				c := a[j]
				if strings.IndexByte(curlShortFlags, c) >= 0 {
					continue
				}
				if strings.IndexByte(curlShortFlagsWithValue, c) < 0 {
					return false
				}
				switch {
				case j+1 < len(a):
					value = a[j+1:]
				case i+1 < len(args):
					i++
					value = args[i]
				default:
					return false
				}
				hasValue = true
				break
			}
		}
		// -w '%output{file}' (curl >= 8.3) writes the rest of the output to a file.
		if hasValue && strings.Contains(value, "%output") {
			return false
		}
	}
	return true
}

// IsCurlMutationToken reports whether a single curl argument changes the
// request from a plain read (GET) into one that sends a body or otherwise
// performs a write-shaped operation.
func IsCurlMutationToken(token string) bool {
	if hasFlagWithValue(token, "-d", "--data", "--data-raw", "--data-binary",
		"--data-ascii", "--data-urlencode", "--json", "--form-string",
		"-F", "--form", "-T", "--upload-file", "-X", "--request", "-K", "--config",
		"-Q", "--quote") {
		return true
	}
	return hasMutatingCurlShortOption(token)
}

// hasMutatingCurlShortOption understands curl's bundled short-option syntax.
// Once it reaches an option that consumes an attached value, the remaining
// bytes are data rather than more options: -o/tmp/data and -HHeader:data must
// not be mistaken for -d/-T/-X. Flag-only prefixes still expose a later
// mutation, so -sSXPOST and -sK- remain gated.
func hasMutatingCurlShortOption(token string) bool {
	if len(token) < 2 || token[0] != '-' || strings.HasPrefix(token, "--") {
		return false
	}
	for i := 1; i < len(token); i++ {
		switch token[i] {
		case 'd', 'F', 'T', 'X', 'K', 'Q':
			return true
		case 'A', 'b', 'c', 'C', 'D', 'e', 'E', 'h', 'H', 'm', 'o', 'P',
			'r', 't', 'u', 'U', 'w', 'x', 'y', 'Y', 'z':
			return false
		}
	}
	return false
}

// IsReadOnlyWgetCommand reports whether a wget invocation only fetches to
// stdout (-O-, -qO-, -O -, --output-document=-). Without that, wget saves a
// file; other flags (-o, --save-cookies, --post-data, --method, ...) ask.
func IsReadOnlyWgetCommand(fields []string) bool {
	if len(fields) == 0 || fields[0] != "wget" {
		return false
	}
	toStdout := false
	args := fields[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case !strings.HasPrefix(a, "-"):
			continue // URL
		case a == "-q" || a == "--quiet" || a == "--no-check-certificate":
			continue
		case a == "-O-" || a == "-qO-" || a == "--output-document=-":
			toStdout = true
		case a == "-O" || a == "--output-document":
			if i+1 >= len(args) || args[i+1] != "-" {
				return false
			}
			i++
			toStdout = true
		case a == "-T" || a == "--timeout":
			if i+1 >= len(args) {
				return false
			}
			i++
		case strings.HasPrefix(a, "--timeout=") || (strings.HasPrefix(a, "-T") && len(a) > 2):
			continue
		default:
			return false
		}
	}
	return toStdout
}

// IsReadOnlyNcCommand reports whether an `nc` invocation is a zero-I/O
// connectivity probe (-z), the one shape investigations need. Only -z, -v, -n,
// -u (bundled or separate) and -w <seconds> are allowed; anything else asks,
// including -l (listen) and -e/-c (run a program on the connection).
func IsReadOnlyNcCommand(fields []string) bool {
	if len(fields) == 0 || fields[0] != "nc" {
		return false
	}
	zeroIO := false
	args := fields[1:]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case !strings.HasPrefix(a, "-"):
			continue // host, port
		case a == "-w":
			if i+1 >= len(args) {
				return false
			}
			i++
		case strings.HasPrefix(a, "-w") && len(a) > 2:
			continue
		case len(a) > 1 && a[1] != '-':
			for _, c := range a[1:] {
				switch c {
				case 'z':
					zeroIO = true
				case 'v', 'n', 'u':
				default:
					return false
				}
			}
		default:
			return false
		}
	}
	return zeroIO
}

func hasFlagWithValue(token string, flags ...string) bool {
	for _, flag := range flags {
		if token == flag || strings.HasPrefix(token, flag+"=") ||
			(len(flag) == 2 && strings.HasPrefix(token, flag) && len(token) > len(flag)) {
			return true
		}
	}
	return false
}
