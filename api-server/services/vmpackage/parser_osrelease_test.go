package vmpackage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOSRelease(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantFamily string
		wantVer    string
	}{
		{
			name: "ubuntu",
			raw: "NAME=\"Ubuntu\"\nVERSION_ID=\"22.04\"\nID=ubuntu\nID_LIKE=debian\n" +
				"VERSION_CODENAME=jammy\n",
			wantFamily: "ubuntu",
			wantVer:    "22.04",
		},
		{
			name:       "debian",
			raw:        "PRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\nNAME=\"Debian GNU/Linux\"\nVERSION_ID=\"12\"\nID=debian\n",
			wantFamily: "debian",
			wantVer:    "12",
		},
		{
			name:       "rhel",
			raw:        "NAME=\"Red Hat Enterprise Linux\"\nVERSION_ID=\"9.3\"\nID=\"rhel\"\nID_LIKE=\"fedora\"\n",
			wantFamily: "redhat",
			wantVer:    "9.3",
		},
		{
			name:       "amazon linux 2023",
			raw:        "NAME=\"Amazon Linux\"\nVERSION_ID=\"2023\"\nID=\"amzn\"\nID_LIKE=\"fedora\"\n",
			wantFamily: "amazonlinux",
			wantVer:    "2023",
		},
		{
			name:       "rocky derives via id_like",
			raw:        "NAME=\"Rocky Linux\"\nVERSION_ID=\"9.3\"\nID=\"rocky\"\nID_LIKE=\"rhel centos fedora\"\n",
			wantFamily: "redhat",
			wantVer:    "9.3",
		},
		{
			// Regression: fedora and Oracle Linux publish their own advisories
			// and are separate families in vuln-matcher-server's loaded DB —
			// verified against a live GET /v1/capabilities response. Must NOT
			// collapse into "redhat".
			name:       "fedora stays its own family",
			raw:        "NAME=\"Fedora Linux\"\nVERSION_ID=\"40\"\nID=fedora\n",
			wantFamily: "fedora",
			wantVer:    "40",
		},
		{
			name:       "oracle linux stays its own family",
			raw:        "NAME=\"Oracle Linux Server\"\nVERSION_ID=\"9.3\"\nID=\"ol\"\nID_LIKE=\"fedora\"\n",
			wantFamily: "oraclelinux",
			wantVer:    "9.3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			family, version, err := ParseOSRelease(tc.raw)
			require.NoError(t, err)
			assert.Equal(t, tc.wantFamily, family)
			assert.Equal(t, tc.wantVer, version)
		})
	}
}

func TestParseOSRelease_MissingFields(t *testing.T) {
	_, _, err := ParseOSRelease("NAME=\"Ubuntu\"\n")
	assert.Error(t, err)

	_, _, err = ParseOSRelease("ID=ubuntu\n")
	assert.Error(t, err)
}
