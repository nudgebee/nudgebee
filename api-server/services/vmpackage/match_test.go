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
