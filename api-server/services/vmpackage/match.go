package vmpackage

import (
	"strconv"
	"strings"

	"nudgebee/services/vulnmatcher"
)

// packageKey is a deterministic identity string for a package tuple — the
// same tuple vm_package's unique index conflicts on. Used as the
// vuln-matcher-server request Key so a Finding can be mapped back to the
// Package that produced it without a second DB round-trip.
func packageKey(p Package) string {
	epochStr := ""
	if p.Epoch != nil {
		epochStr = strconv.Itoa(*p.Epoch)
	}
	return strings.Join([]string{p.Type, p.Name, p.Version, p.Arch, epochStr, p.SourceName}, "|")
}

// buildMatchRequest dedupes packages by identity and builds a vuln-matcher
// MatchRequest, alongside a lookup from each package's Key back to the
// Package that produced it (for turning Findings back into recommendation
// rows).
func buildMatchRequest(osFamily, osVersion string, pkgs []Package) (vulnmatcher.MatchRequest, map[string]Package) {
	seen := make(map[string]struct{}, len(pkgs))
	pkgsByKey := make(map[string]Package, len(pkgs))
	vmPkgs := make([]vulnmatcher.Package, 0, len(pkgs))

	for _, p := range pkgs {
		key := packageKey(p)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		pkgsByKey[key] = p

		vmPkgs = append(vmPkgs, vulnmatcher.Package{
			Key:           key,
			Name:          p.Name,
			Type:          p.Type,
			Version:       p.Version,
			Arch:          p.Arch,
			Epoch:         p.Epoch,
			SourceName:    p.SourceName,
			SourceVersion: p.SourceVersion,
		})
	}

	req := vulnmatcher.MatchRequest{
		OS:       vulnmatcher.OS{Family: osFamily, Version: osVersion},
		Packages: vmPkgs,
	}
	return req, pkgsByKey
}
