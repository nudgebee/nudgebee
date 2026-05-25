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

func firstShellToken(lower string) string {
	trimmed := strings.TrimLeft(lower, " \t")
	if i := strings.IndexAny(trimmed, " \t;|&\n"); i >= 0 {
		return trimmed[:i]
	}
	return trimmed
}

func containsLabel(ss []string, target string) bool {
	for _, s := range ss {
		if s == target {
			return true
		}
	}
	return false
}
