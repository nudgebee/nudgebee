package vmpackage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRPMQA(t *testing.T) {
	// Real discovery_inventory "pkgs-rpm" collector shape: name, epoch,
	// version, release, arch, sourcerpm, <unused>, <installtime>.
	raw := "openssl\t1\t3.5.5\t1.amzn2023.0.5\tx86_64\topenssl-3.5.5-1.amzn2023.0.5.src.rpm\t(none)\t1784931981\n" +
		"perl-FileHandle\t0\t2.03\t477.amzn2023.0.9\tnoarch\tperl-5.32.1-477.amzn2023.0.9.src.rpm\t(none)\t1784931981\n" +
		"vim-filesystem\t2\t9.2.725\t1.amzn2023.0.1\tnoarch\tvim-9.2.725-1.amzn2023.0.1.src.rpm\t(none)\t1784931953\n"

	pkgs, err := ParseRPMQA(raw)
	require.NoError(t, err)
	require.Len(t, pkgs, 3)

	// explicit epoch parses to the correct *int, version+release combine
	// into vuln-matcher-server's expected "version-release" shape.
	require.NotNil(t, pkgs[0].Epoch)
	assert.Equal(t, 1, *pkgs[0].Epoch)
	assert.Equal(t, "3.5.5-1.amzn2023.0.5", pkgs[0].Version)
	assert.Equal(t, "openssl", pkgs[0].SourceName)
	assert.Equal(t, "3.5.5-1.amzn2023.0.5", pkgs[0].SourceVersion)

	// epoch 0 is explicit here (this content pack normalizes missing epoch
	// to "0" rather than sending "(none)") — still a valid, non-nil epoch.
	require.NotNil(t, pkgs[1].Epoch)
	assert.Equal(t, 0, *pkgs[1].Epoch)
	// source rpm name differs from the binary package (perl-FileHandle -> perl).
	assert.Equal(t, "perl", pkgs[1].SourceName)

	require.NotNil(t, pkgs[2].Epoch)
	assert.Equal(t, 2, *pkgs[2].Epoch)
	assert.Equal(t, "vim", pkgs[2].SourceName)
	assert.Equal(t, "9.2.725-1.amzn2023.0.1", pkgs[2].SourceVersion)
}

// TestParseRPMQA_SourceVersionDiffersFromBinaryVersion pins #36278. perl ships
// as ~20 subpackages that each carry their own small version (perl-FileHandle
// is 2.03) while every advisory is filed against perl 5.32.1. Dropping
// SourceVersion made vuln-matcher-server fall back to the binary version, so
// 2.03 was compared against the advisory's 5.32.1 and the package was reported
// vulnerable on a fully-patched host, permanently. Verified on a live
// Amazon Linux 2023 host: perl-B 1.80-477.amzn2023.0.9 from
// perl-5.32.1-477.amzn2023.0.9.src.rpm, with `dnf check-update perl*` clean.
func TestParseRPMQA_SourceVersionDiffersFromBinaryVersion(t *testing.T) {
	pkgs, err := ParseRPMQA(
		"perl-B\t0\t1.80\t477.amzn2023.0.9\tx86_64\tperl-5.32.1-477.amzn2023.0.9.src.rpm\t(none)\t1784931981\n" +
			"perl-FileHandle\t0\t2.03\t477.amzn2023.0.9\tnoarch\tperl-5.32.1-477.amzn2023.0.9.src.rpm\t(none)\t1784931981\n")
	require.NoError(t, err)
	require.Len(t, pkgs, 2)

	for _, p := range pkgs {
		assert.Equal(t, "perl", p.SourceName)
		// The advisory's fixed_version is expressed in this vocabulary, so this
		// is the value the comparison has to be made against.
		assert.Equal(t, "5.32.1-477.amzn2023.0.9", p.SourceVersion)
		// ...and it must NOT be the binary package's own version.
		assert.NotEqual(t, p.Version, p.SourceVersion)
	}
}

// TestParseRPMQA_HyphenatedSourceName guards the split: rpm forbids "-" in
// version and release but allows it in the name, so the name must absorb every
// hyphen except the last two separators.
func TestParseRPMQA_HyphenatedSourceName(t *testing.T) {
	pkgs, err := ParseRPMQA(
		"NetworkManager-libnm\t1\t1.48.10\t5.el9_5\tx86_64\tNetworkManager-1.48.10-5.el9_5.src.rpm\t(none)\t1784931981\n" +
			"rpm-plugin-selinux\t0\t4.16.1.3\t34.amzn2023.0.6\tx86_64\trpm-4.16.1.3-34.amzn2023.0.6.src.rpm\t(none)\t1784931981\n" +
			"python3-setuptools-wheel\t0\t53.0.0\t13.el9\tnoarch\tpython-setuptools-53.0.0-13.el9.src.rpm\t(none)\t1784931981\n")
	require.NoError(t, err)
	require.Len(t, pkgs, 3)

	assert.Equal(t, "NetworkManager", pkgs[0].SourceName)
	assert.Equal(t, "1.48.10-5.el9_5", pkgs[0].SourceVersion)

	assert.Equal(t, "rpm", pkgs[1].SourceName)
	assert.Equal(t, "4.16.1.3-34.amzn2023.0.6", pkgs[1].SourceVersion)

	// Source name itself contains a hyphen.
	assert.Equal(t, "python-setuptools", pkgs[2].SourceName)
	assert.Equal(t, "53.0.0-13.el9", pkgs[2].SourceVersion)
}

func TestParseRPMQA_ToleratesNoneEpochDefensively(t *testing.T) {
	// Not observed in real discovery_inventory output (this content pack
	// always sends a digit), but tolerated in case a different pack version
	// or OS ever sends the unnormalized rpm "(none)" form.
	pkgs, err := ParseRPMQA("bash\t(none)\t5.1.8\t6.el9_1\tx86_64\tbash-5.1.8-6.el9_1.src.rpm\t(none)\t1784931981\n")
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Nil(t, pkgs[0].Epoch)
	assert.Equal(t, "5.1.8-6.el9_1", pkgs[0].Version)
}

func TestParseRPMQA_MissingSourceRPMFallsBackToName(t *testing.T) {
	pkgs, err := ParseRPMQA("gpg-pubkey\t0\t2fa658e0\t45700c69\tnoarch\t(none)\t(none)\t1784931981\n")
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "gpg-pubkey", pkgs[0].SourceName)
	// With no SOURCERPM to read, the binary package's own identity is the only
	// thing available — SourceVersion must still be set rather than left empty,
	// since empty is what triggers the bad fallback in vuln-matcher-server.
	assert.Equal(t, "2fa658e0-45700c69", pkgs[0].SourceVersion)
}

// TestParseRPMQA_UnparseableSourceRPMFallsBack covers a SOURCERPM that is
// present but does not match the expected shape — same fallback as "(none)".
func TestParseRPMQA_UnparseableSourceRPMFallsBack(t *testing.T) {
	pkgs, err := ParseRPMQA("weird\t0\t1.0\t1.el9\tx86_64\tnot-a-source-rpm\t(none)\t1784931981\n")
	require.NoError(t, err)
	require.Len(t, pkgs, 1)
	assert.Equal(t, "weird", pkgs[0].SourceName)
	assert.Equal(t, "1.0-1.el9", pkgs[0].SourceVersion)
}

func TestParseRPMQA_InvalidEpoch(t *testing.T) {
	_, err := ParseRPMQA("bash\tnot-a-number\t5.1.8\t6.el9_1\tx86_64\tbash-5.1.8-6.el9_1.src.rpm\t(none)\t1784931981\n")
	assert.Error(t, err)
}

func TestParseRPMQA_MalformedLine(t *testing.T) {
	_, err := ParseRPMQA("bash\t0\t5.1.8\n")
	assert.Error(t, err)
}
