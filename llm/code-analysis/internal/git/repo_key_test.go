package git

import "testing"

func TestIsSafeRepoKey(t *testing.T) {
	for _, tc := range []struct {
		url  string
		safe bool
	}{
		{"https://github.com/acme/payments.git", true},
		{"git@github.com:acme/payments.git", true},
		{"..", false},
		{".", false},
		{"", false},
		{"https://..", false},
	} {
		if got := isSafeRepoKey(repoKeyFromURL(tc.url)); got != tc.safe {
			t.Errorf("isSafeRepoKey(repoKeyFromURL(%q)) = %v, want %v", tc.url, got, tc.safe)
		}
	}
}
