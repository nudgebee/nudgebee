package git

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInjectTokenIntoURL(t *testing.T) {
	cases := []struct {
		name    string
		repoURL string
		token   string
		want    string
	}{
		{"github", "https://github.com/org/repo.git", "tok", "https://x-access-token:tok@github.com/org/repo.git"},
		{"gitlab", "https://gitlab.com/grp/proj.git", "tok", "https://oauth2:tok@gitlab.com/grp/proj.git"},
		{"empty token is a no-op", "https://github.com/org/repo.git", "", "https://github.com/org/repo.git"},
		{"non-https is a no-op", "git@github.com:org/repo.git", "tok", "git@github.com:org/repo.git"},
		{"existing userinfo is replaced, not double-embedded", "https://x-access-token:old@github.com/org/repo.git", "new", "https://x-access-token:new@github.com/org/repo.git"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, InjectTokenIntoURL(tc.repoURL, tc.token))
		})
	}
}

// A token with RFC 3986 sub-delims / reserved chars must be percent-encoded so git
// parses it as a single credential. Verify it round-trips and is not stored raw.
func TestInjectTokenIntoURLEncodesSpecialChars(t *testing.T) {
	const token = "abc:de/fg@hi"
	got := InjectTokenIntoURL("https://github.com/org/repo.git", token)

	u, err := url.Parse(got)
	require.NoError(t, err)
	require.Equal(t, "x-access-token", u.User.Username())
	pw, _ := u.User.Password()
	require.Equal(t, token, pw) // decodes back to the original token
	require.NotContains(t, got, token)
	require.Equal(t, "github.com", u.Host)
}

func TestStripURLUserinfo(t *testing.T) {
	require.Equal(t, "https://github.com/org/repo.git", StripURLUserinfo("https://x-access-token:tok@github.com/org/repo.git"))
	require.Equal(t, "https://github.com/org/repo.git", StripURLUserinfo("https://github.com/org/repo.git"))
	require.Equal(t, "git@github.com:org/repo.git", StripURLUserinfo("git@github.com:org/repo.git"))
}
