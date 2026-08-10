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

	// epoch 0 is explicit here (this content pack normalizes missing epoch
	// to "0" rather than sending "(none)") — still a valid, non-nil epoch.
	require.NotNil(t, pkgs[1].Epoch)
	assert.Equal(t, 0, *pkgs[1].Epoch)
	// source rpm name differs from the binary package (perl-FileHandle -> perl).
	assert.Equal(t, "perl", pkgs[1].SourceName)

	require.NotNil(t, pkgs[2].Epoch)
	assert.Equal(t, 2, *pkgs[2].Epoch)
	assert.Equal(t, "vim", pkgs[2].SourceName)
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
}

func TestParseRPMQA_InvalidEpoch(t *testing.T) {
	_, err := ParseRPMQA("bash\tnot-a-number\t5.1.8\t6.el9_1\tx86_64\tbash-5.1.8-6.el9_1.src.rpm\t(none)\t1784931981\n")
	assert.Error(t, err)
}

func TestParseRPMQA_MalformedLine(t *testing.T) {
	_, err := ParseRPMQA("bash\t0\t5.1.8\n")
	assert.Error(t, err)
}
