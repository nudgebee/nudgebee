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
// rows). Non-runtime kernel packages are left out of the request entirely
// (see isNonRuntimeKernelPackage) — they're still stored in vm_package by
// upsertPackages, this only affects what gets matched.
func buildMatchRequest(osFamily, osVersion string, pkgs []Package) (vulnmatcher.MatchRequest, map[string]Package) {
	seen := make(map[string]struct{}, len(pkgs))
	pkgsByKey := make(map[string]Package, len(pkgs))
	vmPkgs := make([]vulnmatcher.Package, 0, len(pkgs))

	for _, p := range pkgs {
		if isNonRuntimeKernelPackage(p) {
			continue
		}

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

// nonRuntimeKernelMarkers are substrings that, found in a kernel-family
// package name, mean the package doesn't ship code that runs on the host —
// build headers, userspace diagnostic tools, documentation, source tarballs,
// debug symbols. Grype has no concept of "kernel line" (confirmed against
// grype/distro.Distro's fields: Type/Version/Codename/Channels/IDLike, and a
// repo-wide grep for "kernel" — nothing) so distro-aware matching alone
// cannot bound this: kernel CVEs are routinely left with no upper-bound fixed
// version in the tracker data grype ingests, so a package sharing the
// kernel's advisory identity inherits the source package's *entire* CVE
// history, unbounded, forever. See #36279 — on a live host, linux-tools-common
// alone (never executes; it's perf/cpupower/etc.) carried 2,481 of 16,266
// open findings spanning CVE years 2012-2026.
//
// These packages are still recorded in vm_package by upsertPackages (this
// only trims what's sent to the matcher), so inventory/asset views are
// unaffected.
var nonRuntimeKernelMarkers = []string{
	"-headers", // also covers "-cross-headers"
	"-tools",
	"-devel", // also covers "-debug-devel"
	"-doc",
	"-debuginfo",
	"-buildinfo",
	"-abi-whitelists", "-abi-stablelists",
}

// nonRuntimeKernelPrefixes are whole-package matches or prefixes that don't
// fit the marker-substring shape above.
var nonRuntimeKernelPrefixes = []string{
	"linux-source", // linux-source, linux-source-5.15.0
	"linux-udebs",  // debian-installer only, not present on a running host
}

// isNonRuntimeKernelPackage reports whether p is a kernel-family package
// (dpkg's "linux[-flavor]" source tree, rpm's "kernel" source tree) that
// doesn't represent code actually running on the host — as opposed to
// linux-image-*/linux-modules-* (dpkg) or kernel/kernel-core/kernel-modules
// (rpm), which do and must still be matched.
func isNonRuntimeKernelPackage(p Package) bool {
	name := strings.ToLower(p.Name)

	switch p.Type {
	case PkgTypeDeb:
		if name != "linux" && !strings.HasPrefix(name, "linux-") {
			return false
		}
	case PkgTypeRPM:
		if name != "kernel" && !strings.HasPrefix(name, "kernel-") {
			return false
		}
	default:
		return false
	}

	if name == "linux-libc-dev" {
		return true
	}

	for _, prefix := range nonRuntimeKernelPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	for _, marker := range nonRuntimeKernelMarkers {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}
