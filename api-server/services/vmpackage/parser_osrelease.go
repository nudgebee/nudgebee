package vmpackage

import "strings"

// ParseOSRelease parses the KEY=value lines of /etc/os-release (or
// /usr/lib/os-release) and returns the normalized OS family vuln-matcher-server
// expects plus the raw VERSION_ID. Values may be double-quoted per the
// os-release spec; quotes are stripped.
//
// Family normalization is a best-effort mapping from the distro ID to
// vuln-matcher-server's family vocabulary — callers must still confirm
// coverage against GET /v1/capabilities before calling /v1/match, since the
// loaded vulnerability database is the actual source of truth.
func ParseOSRelease(raw string) (family, version string, err error) {
	fields := parseOSReleaseFields(raw)

	id := fields["ID"]
	if id == "" {
		return "", "", &parseError{stage: "os-release", reason: "missing ID field"}
	}
	version = fields["VERSION_ID"]
	if version == "" {
		return "", "", &parseError{stage: "os-release", reason: "missing VERSION_ID field"}
	}

	return normalizeFamily(id, fields["ID_LIKE"]), version, nil
}

func parseOSReleaseFields(raw string) map[string]string {
	fields := make(map[string]string)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"'`)
		fields[strings.TrimSpace(key)] = value
	}
	return fields
}

// redhatLikeFamilies are distro IDs that consume RHEL's vulnerability
// advisories (RHSA/RHBA) and share vuln-matcher-server's "redhat" family —
// verified against a live GET /v1/capabilities response. Fedora, Oracle
// Linux, and Amazon Linux publish their own advisories and are separate
// families in the loaded DB, not folded into "redhat".
var redhatLikeFamilies = map[string]bool{
	"rhel":      true,
	"centos":    true,
	"rocky":     true,
	"almalinux": true,
}

// directFamilies are distro IDs whose vuln-matcher-server family name is a
// straight rename (or identical) to the os-release ID.
var directFamilies = map[string]string{
	"fedora":        "fedora",
	"ol":            "oraclelinux",
	"oraclelinux":   "oraclelinux",
	"amzn":          "amazonlinux",
	"amazon":        "amazonlinux",
	"sles":          "sles",
	"opensuse":      "sles",
	"opensuse-leap": "sles",
	"ubuntu":        "ubuntu",
	"debian":        "debian",
	"alpine":        "alpine",
}

func normalizeFamily(id string, idLike string) string {
	id = strings.ToLower(id)
	if redhatLikeFamilies[id] {
		return "redhat"
	}
	if fam, ok := directFamilies[id]; ok {
		return fam
	}

	// Unrecognized ID — fall back to ID_LIKE, which os-release defines as a
	// space-separated list of IDs the distro derives from.
	for _, like := range strings.Fields(idLike) {
		like = strings.ToLower(like)
		if redhatLikeFamilies[like] {
			return "redhat"
		}
		if fam, ok := directFamilies[like]; ok {
			return fam
		}
	}
	return id
}

type parseError struct {
	stage  string
	reason string
}

func (e *parseError) Error() string {
	return "vmpackage: " + e.stage + ": " + e.reason
}
