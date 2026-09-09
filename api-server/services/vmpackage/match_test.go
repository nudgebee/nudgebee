package vmpackage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildMatchRequest_DedupesAndPopulatesLookup(t *testing.T) {
	epoch := 1
	pkgs := []Package{
		{Type: PkgTypeRPM, Name: "openssl", Version: "3.0.7-24.el9", Arch: "x86_64", Epoch: &epoch, SourceName: "openssl"},
		// exact duplicate — same identity tuple, should collapse to one.
		{Type: PkgTypeRPM, Name: "openssl", Version: "3.0.7-24.el9", Arch: "x86_64", Epoch: &epoch, SourceName: "openssl"},
		{Type: PkgTypeDeb, Name: "bash", Version: "5.1-6ubuntu1", Arch: "amd64", SourceName: "bash"},
	}

	req, pkgsByKey := buildMatchRequest("redhat", "9", pkgs)

	require.Len(t, req.Packages, 2)
	assert.Equal(t, "redhat", req.OS.Family)
	assert.Equal(t, "9", req.OS.Version)

	require.Len(t, pkgsByKey, 2)
	for _, vp := range req.Packages {
		pkg, ok := pkgsByKey[vp.Key]
		require.True(t, ok)
		assert.Equal(t, pkg.Name, vp.Name)
		assert.Equal(t, pkg.SourceName, vp.SourceName)
	}
}

func TestPackageKey_DistinguishesNilFromZeroEpoch(t *testing.T) {
	zero := 0
	noEpoch := Package{Type: PkgTypeRPM, Name: "foo", Version: "1.0", SourceName: "foo"}
	zeroEpoch := Package{Type: PkgTypeRPM, Name: "foo", Version: "1.0", SourceName: "foo", Epoch: &zero}

	assert.NotEqual(t, packageKey(noEpoch), packageKey(zeroEpoch))
}

// TestIsNonRuntimeKernelPackage pins #36279's dpkg reproduction: on a live
// host, linux-tools-common (never executes — it's perf/cpupower/etc., built
// from the "linux" source) carried 2,481 of 16,266 open findings, inheriting
// the kernel source's entire CVE history back to 2012 because grype has no
// concept of "kernel line" to bound the match. Packages that do represent
// running code (linux-image-*, linux-modules-*, kernel, kernel-core,
// kernel-modules) must never be excluded here — that would create false
// negatives instead of false positives.
func TestIsNonRuntimeKernelPackage(t *testing.T) {
	tests := []struct {
		name string
		pkg  Package
		want bool
	}{
		// dpkg: non-runtime, must be excluded.
		{"dpkg linux-tools-common", Package{Type: PkgTypeDeb, Name: "linux-tools-common"}, true},
		{"dpkg linux-tools-5.15.0-187-generic", Package{Type: PkgTypeDeb, Name: "linux-tools-5.15.0-187-generic"}, true},
		{"dpkg linux-headers-generic", Package{Type: PkgTypeDeb, Name: "linux-headers-generic"}, true},
		{"dpkg linux-headers-6.8.0-1040-aws", Package{Type: PkgTypeDeb, Name: "linux-headers-6.8.0-1040-aws"}, true},
		{"dpkg linux-aws-6.8-tools-6.8.0-1040", Package{Type: PkgTypeDeb, Name: "linux-aws-6.8-tools-6.8.0-1040"}, true},
		{"dpkg linux-doc", Package{Type: PkgTypeDeb, Name: "linux-doc"}, true},
		{"dpkg linux-source-5.15.0", Package{Type: PkgTypeDeb, Name: "linux-source-5.15.0"}, true},
		{"dpkg linux-libc-dev", Package{Type: PkgTypeDeb, Name: "linux-libc-dev"}, true},
		{"dpkg linux-buildinfo", Package{Type: PkgTypeDeb, Name: "linux-buildinfo-5.15.0-187-generic"}, true},
		{"dpkg linux-udebs-generic", Package{Type: PkgTypeDeb, Name: "linux-udebs-generic"}, true},

		// dpkg: runtime, must NOT be excluded.
		{"dpkg linux-image-6.8.0-1061-aws", Package{Type: PkgTypeDeb, Name: "linux-image-6.8.0-1061-aws"}, false},
		{"dpkg linux-modules-6.8.0-1061-aws", Package{Type: PkgTypeDeb, Name: "linux-modules-6.8.0-1061-aws"}, false},
		{"dpkg linux-modules-extra-6.8.0-1061-aws", Package{Type: PkgTypeDeb, Name: "linux-modules-extra-6.8.0-1061-aws"}, false},
		{"dpkg linux-restricted-modules-aws", Package{Type: PkgTypeDeb, Name: "linux-restricted-modules-aws"}, false},
		{"dpkg linux-aws meta", Package{Type: PkgTypeDeb, Name: "linux-aws"}, false},
		{"dpkg non-kernel package", Package{Type: PkgTypeDeb, Name: "openssl"}, false},

		// rpm: non-runtime, must be excluded.
		{"rpm kernel-headers", Package{Type: PkgTypeRPM, Name: "kernel-headers"}, true},
		{"rpm kernel-devel", Package{Type: PkgTypeRPM, Name: "kernel-devel"}, true},
		{"rpm kernel-debug-devel", Package{Type: PkgTypeRPM, Name: "kernel-debug-devel"}, true},
		{"rpm kernel-tools", Package{Type: PkgTypeRPM, Name: "kernel-tools"}, true},
		{"rpm kernel-tools-libs", Package{Type: PkgTypeRPM, Name: "kernel-tools-libs"}, true},
		{"rpm kernel-doc", Package{Type: PkgTypeRPM, Name: "kernel-doc"}, true},
		{"rpm kernel-debuginfo", Package{Type: PkgTypeRPM, Name: "kernel-debuginfo"}, true},
		{"rpm kernel-abi-whitelists", Package{Type: PkgTypeRPM, Name: "kernel-abi-whitelists"}, true},
		{"rpm kernel-cross-headers", Package{Type: PkgTypeRPM, Name: "kernel-cross-headers"}, true},

		// rpm: runtime, must NOT be excluded.
		{"rpm bare kernel", Package{Type: PkgTypeRPM, Name: "kernel"}, false},
		{"rpm kernel-core", Package{Type: PkgTypeRPM, Name: "kernel-core"}, false},
		{"rpm kernel-modules", Package{Type: PkgTypeRPM, Name: "kernel-modules"}, false},
		{"rpm kernel-modules-extra", Package{Type: PkgTypeRPM, Name: "kernel-modules-extra"}, false},
		{"rpm kernel-debug", Package{Type: PkgTypeRPM, Name: "kernel-debug"}, false},
		{"rpm non-kernel package", Package{Type: PkgTypeRPM, Name: "openssl"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isNonRuntimeKernelPackage(tt.pkg))
		})
	}
}

// TestBuildMatchRequest_ExcludesNonRuntimeKernelPackages confirms the
// exclusion is actually wired into buildMatchRequest, not just the
// standalone predicate.
func TestBuildMatchRequest_ExcludesNonRuntimeKernelPackages(t *testing.T) {
	pkgs := []Package{
		{Type: PkgTypeDeb, Name: "linux-tools-common", Version: "5.15.0-187.197", SourceName: "linux"},
		{Type: PkgTypeDeb, Name: "linux-image-generic", Version: "5.15.0.187.177", SourceName: "linux-meta"},
	}

	req, pkgsByKey := buildMatchRequest("ubuntu", "22.04", pkgs)

	require.Len(t, req.Packages, 1)
	assert.Equal(t, "linux-image-generic", req.Packages[0].Name)
	require.Len(t, pkgsByKey, 1)
}
