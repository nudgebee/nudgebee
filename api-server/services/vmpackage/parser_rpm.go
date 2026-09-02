package vmpackage

import (
	"regexp"
	"strconv"
	"strings"
)

// sourceRPMPattern splits a SOURCERPM value ("openssl-3.5.5-1.amzn2023.0.5.src.rpm")
// into its source package name and its "version-release". rpm forbids "-" in
// version and release but allows it in the name, so the last two hyphen-separated
// segments are always version and release, and everything before them is the name.
var sourceRPMPattern = regexp.MustCompile(`^(.+)-([^-]+-[^-]+)\.src\.rpm$`)

// ParseRPMQA parses discovery_inventory's "pkgs-rpm" collector output:
// tab-separated name/epoch/version/release/arch/sourcerpm/<unused>/<installtime>,
// one package per line. Version and release are combined into
// vuln-matcher-server's expected "version-release" shape
// ("3.5.5-1.amzn2023.0.5") — the release suffix matters for CVE matching.
func ParseRPMQA(raw string) ([]Package, error) {
	var pkgs []Package
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 6 {
			return nil, &parseError{stage: "pkgs-rpm", reason: "malformed line: " + line}
		}

		name, epochStr, version, release, arch, sourceRPM := fields[0], fields[1], fields[2], fields[3], fields[4], fields[5]

		epoch, err := parseRPMEpoch(epochStr)
		if err != nil {
			return nil, &parseError{stage: "pkgs-rpm", reason: "invalid epoch for " + name + ": " + err.Error()}
		}

		// SourceVersion matters as much as SourceName: advisories are published
		// against the source package, and vuln-matcher-server falls back to the
		// *binary* package's version when SourceVersion is empty (see
		// toGrypePackage in vulnerability-server/internal/engine/engine.go).
		// For a package whose binary version differs from its source version —
		// perl-FileHandle 2.03 built from perl 5.32.1 being the canonical case —
		// that fallback compares 2.03 against the advisory's 5.32.1 and reports
		// the package vulnerable no matter how patched the host is. See #36278.
		sourceName, sourceVersion := sourceRPMToNameVersion(sourceRPM)
		if sourceName == "" {
			sourceName = name
			sourceVersion = version + "-" + release
		}

		pkgs = append(pkgs, Package{
			Type:          PkgTypeRPM,
			Name:          name,
			Version:       version + "-" + release,
			Arch:          arch,
			Epoch:         epoch,
			SourceName:    sourceName,
			SourceVersion: sourceVersion,
		})
	}
	return pkgs, nil
}

// parseRPMEpoch parses the epoch field. This content pack normalizes a
// missing epoch to the literal "0" before it ever reaches services-server
// (verified against a live discovery_inventory sample) — but "(none)"/empty
// are still tolerated defensively as nil, matching vuln-matcher-server's own
// nil-vs-zero distinction, in case a different pack version or OS ever sends
// the unnormalized form.
func parseRPMEpoch(raw string) (*int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "(none)" {
		return nil, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// sourceRPMToNameVersion splits a SOURCERPM value into its source package name
// and "version-release". Returns ("", "") when the field is absent ("(none)",
// as rpm reports for packages built without one, e.g. gpg-pubkey) or does not
// parse, leaving the caller to fall back to the binary package's own identity.
func sourceRPMToNameVersion(sourceRPM string) (string, string) {
	sourceRPM = strings.TrimSpace(sourceRPM)
	if sourceRPM == "" || sourceRPM == "(none)" {
		return "", ""
	}
	m := sourceRPMPattern.FindStringSubmatch(sourceRPM)
	if m == nil {
		return "", ""
	}
	return m[1], m[2]
}
