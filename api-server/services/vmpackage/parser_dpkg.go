package vmpackage

import "strings"

// ParseDpkgQuery parses discovery_inventory's "pkgs-dpkg" collector output:
// tab-separated name/version/arch/status/source_name/source_version, one
// package per line. Only "installed" rows are kept — dpkg also lists
// removed-but-config-remains ("rc") and other non-present statuses, which
// aren't actually on the host. Version is kept verbatim, including any
// epoch prefix ("1:2.0.33-1ubuntu1"), since deb packages carry epoch inline
// rather than as a separate field.
func ParseDpkgQuery(raw string) ([]Package, error) {
	var pkgs []Package
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 6 {
			return nil, &parseError{stage: "pkgs-dpkg", reason: "malformed line: " + line}
		}

		name, version, arch, status, sourceName, sourceVersion := fields[0], fields[1], fields[2], fields[3], fields[4], fields[5]
		if status != "installed" {
			continue
		}
		if sourceName == "" {
			sourceName = name
		}

		pkgs = append(pkgs, Package{
			Type:          PkgTypeDeb,
			Name:          name,
			Version:       version,
			Arch:          arch,
			SourceName:    sourceName,
			SourceVersion: sourceVersion,
		})
	}
	return pkgs, nil
}
