package vmpackage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDpkgQuery(t *testing.T) {
	// Real discovery_inventory "pkgs-dpkg" collector shape: name, version,
	// arch, status, source_name, source_version.
	raw := "bash\t5.1-6ubuntu1.1\tamd64\tinstalled\tbash\t5.1-6ubuntu1.1\n" +
		"acpid\t1:2.0.33-1ubuntu1\tamd64\tinstalled\tacpid\t1:2.0.33-1ubuntu1\n" +
		"apt-utils\t2.4.14\tamd64\tinstalled\tapt\t2.4.14\n" + // binary name differs from source name
		"libfoo1\t1.2.3\tamd64\tinstalled\t\t\n" + // no separate source package -> defaults to name
		"old-removed-pkg\t1.0\tamd64\trc\told-removed-pkg\t1.0\n" // removed-but-config-remains -> dropped

	pkgs, err := ParseDpkgQuery(raw)
	require.NoError(t, err)
	require.Len(t, pkgs, 4)

	assert.Equal(t, Package{
		Type: PkgTypeDeb, Name: "bash", Version: "5.1-6ubuntu1.1", Arch: "amd64",
		SourceName: "bash", SourceVersion: "5.1-6ubuntu1.1",
	}, pkgs[0])

	// epoch-prefixed version must survive byte-exact, embedded in Version
	// (deb carries epoch inline, not as a separate field).
	assert.Equal(t, "1:2.0.33-1ubuntu1", pkgs[1].Version)
	assert.Nil(t, pkgs[1].Epoch)

	// forager's collector already resolves the source package when it
	// differs from the binary package (apt-utils -> apt).
	assert.Equal(t, "apt", pkgs[2].SourceName)

	// empty source_name field falls back to the binary package name.
	assert.Equal(t, "libfoo1", pkgs[3].SourceName)
}

func TestParseDpkgQuery_SkipsNonInstalledStatus(t *testing.T) {
	pkgs, err := ParseDpkgQuery(
		"bash\t5.1-6ubuntu1\tamd64\tinstalled\tbash\t5.1-6ubuntu1\n" +
			"removed-pkg\t1.0\tamd64\tnot-installed\tremoved-pkg\t1.0\n")
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "bash", pkgs[0].Name)
}

func TestParseDpkgQuery_SkipsBlankLines(t *testing.T) {
	pkgs, err := ParseDpkgQuery("bash\t5.1-6ubuntu1\tamd64\tinstalled\tbash\t5.1-6ubuntu1\n\n\n")
	require.NoError(t, err)
	assert.Len(t, pkgs, 1)
}

func TestParseDpkgQuery_MalformedLine(t *testing.T) {
	_, err := ParseDpkgQuery("bash\t5.1-6ubuntu1\n")
	assert.Error(t, err)
}
