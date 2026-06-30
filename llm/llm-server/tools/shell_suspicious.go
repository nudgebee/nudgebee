package tools

import "strings"

// detectSuspiciousShellPatterns returns heuristic labels for recon-style shell
// commands (env dump, /etc/passwd reads, cloud-metadata URLs, credential file
// reads, reverse-shell tricks, history tampering).
//
// This is a defense-in-depth observability signal — trivially bypassable via
// encoding, aliases, or alternate binaries — and MUST NOT be used as a
// security gate. Matches are logged so operators can alert on them.
func detectSuspiciousShellPatterns(command string) []string {
	if command == "" {
		return nil
	}
	lower := strings.ToLower(command)

	var matches []string
	// Bare-keyword invocation; needle-based rule requires surrounding
	// whitespace/pipes and would miss "env" alone on a line.
	if first := firstShellToken(lower); first == "env" || first == "printenv" || first == "set" {
		matches = append(matches, suspiciousLabelEnvDump)
	}

	for _, r := range suspiciousShellRules {
		if r.label == suspiciousLabelEnvDump && containsLabel(matches, suspiciousLabelEnvDump) {
			continue
		}
		for _, n := range r.needles {
			if strings.Contains(lower, n) {
				matches = append(matches, r.label)
				break
			}
		}
	}
	return matches
}

type suspiciousShellRule struct {
	label   string
	needles []string
}

const suspiciousLabelEnvDump = "env_dump"

// Rules live at package scope to avoid re-allocating per call. Read-only.
var suspiciousShellRules = []suspiciousShellRule{
	{label: suspiciousLabelEnvDump, needles: []string{" env ", "\tenv\t", " printenv", "\nprintenv", "printenv ", "printenv\n", "set | grep", "env | grep"}},
	{label: "passwd_read", needles: []string{"/etc/passwd", "/etc/shadow", "/etc/sudoers"}},
	{label: "cloud_metadata", needles: []string{"169.254.169.254", "metadata.google.internal", "169.254.170.2", "metadata.azure.com"}},
	{label: "credential_files", needles: []string{".aws/credentials", ".kube/config", "id_rsa", ".ssh/", "docker/config.json", ".npmrc"}},
	{label: "reverse_shell", needles: []string{"nc -e", "ncat -e", "bash -i ", "/dev/tcp/", "socat tcp", "mkfifo"}},
	{label: "history_tamper", needles: []string{"unset histfile", "history -c", "history -w /dev/null", "export histfile=/dev/null"}},
}

// firstShellToken returns the leading command token of a shell input
// after skipping any leading `VAR=VAL` env-var assignments — so
// `LC_ALL=C grep foo` returns `grep`, `TZ=UTC date` returns `date`.
// A token is treated as an env assignment iff its name (before the
// first `=`) is a valid POSIX shell identifier (letter or underscore,
// then letter / digit / underscore). Returns "" when the input is
// only env prefixes or only whitespace. The leading-env-prefix skip
// is what makes downstream classification (isNoMatchExit /
// firstTokenIsFileReader) robust to common locale forcings like
// `LC_ALL=C`, `LANG=C`, `TZ=UTC` without coupling to an enumerated
// wrapper list (sudo/nohup/time/exec) that is irrelevant in the
// Alpine workspace pod.
func firstShellToken(lower string) string {
	rest := strings.TrimLeft(lower, " \t")
	for rest != "" {
		var token string
		if i := strings.IndexAny(rest, " \t;|&\n"); i >= 0 {
			token = rest[:i]
			rest = strings.TrimLeft(rest[i:], " \t")
		} else {
			token = rest
			rest = ""
		}
		if !isEnvAssignment(token) {
			return token
		}
	}
	return ""
}

// isEnvAssignment reports whether tok is a VAR=VAL env-var prefix,
// where VAR matches the POSIX shell identifier grammar. Conservative
// on purpose: `foo=bar` looks like an env assignment but so does the
// argument of a kubectl label selector if it's the first token (which
// it would not normally be); requiring VAR to be a valid identifier
// avoids skipping things like `123x=foo` that obviously are not env.
func isEnvAssignment(tok string) bool {
	eq := strings.Index(tok, "=")
	if eq <= 0 {
		return false
	}
	for i, r := range tok[:eq] {
		isAlpha := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
		if i == 0 && !isAlpha {
			return false
		}
		if i > 0 && !isAlpha && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func containsLabel(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
