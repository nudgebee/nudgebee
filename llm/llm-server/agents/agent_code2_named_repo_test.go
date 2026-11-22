package agents

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRepoRef(t *testing.T) {
	knownHosts := map[string]bool{"github.com": true}
	tests := []struct {
		name string
		in   string
		want repoRef
		ok   bool
	}{
		{"bare org/repo", "example-org/payments", repoRef{path: "example-org/payments"}, true},
		{"https url", "https://github.com/example-org/payments", repoRef{host: "github.com", path: "example-org/payments"}, true},
		{"url with .git, trailing slash, case, www and port", "https://www.GitHub.com:443/Example-Org/Payments.git/", repoRef{host: "github.com", path: "example-org/payments"}, true},
		{"url with userinfo", "https://bot@github.com/example-org/payments", repoRef{host: "github.com", path: "example-org/payments"}, true},
		{"scp-style remote", "git@github.com:example-org/payments.git", repoRef{host: "github.com", path: "example-org/payments"}, true},
		{"ssh url", "ssh://git@github.com/example-org/payments.git", repoRef{host: "github.com", path: "example-org/payments"}, true},
		{"scheme-less known host", "github.com/example-org/payments", repoRef{host: "github.com", path: "example-org/payments"}, true},
		{"a dotted group is not a host", "team.example/platform/payments", repoRef{path: "team.example/platform/payments"}, true},
		{"gitlab subgroup keeps the full path", "https://gitlab.example.com/platform/billing/payments", repoRef{host: "gitlab.example.com", path: "platform/billing/payments"}, true},
		{"a link into a repository keeps its own longer path", "https://github.com/example-org/payments/actions/runs/1/job/2", repoRef{host: "github.com", path: "example-org/payments/actions/runs/1/job/2"}, true},
		{"url with a query", "https://github.com/example-org/payments?tab=readme", repoRef{}, false},
		{"other scheme", "ftp://github.com/example-org/payments", repoRef{}, false},
		{"stack-trace line", "github.com/example-org/shared-lib/pkg/retry.go:42 +0x1f", repoRef{}, false},
		{"prose", "deploy of example-org/payments failed", repoRef{}, false},
		{"single word", "payments", repoRef{}, false},
		{"image with tag", "example-org/payments:1.4.2", repoRef{}, false},
		{"empty", "", repoRef{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseRepoRef(tt.in, knownHosts)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRepoNamedBy(t *testing.T) {
	repos := []string{
		"https://github.com/example-org/payments",
		"https://github.com/example-org/payments-ui",
		"https://github.com/example-org/shared-lib",
	}
	twoHosts := []string{
		"https://github.com/example-org/payments",
		"https://gitlab.example.com/example-org/payments",
	}
	tests := []struct {
		name   string
		repos  []string
		values []string
		want   string
	}{
		{
			name:   "a label whose whole value is the repository",
			repos:  repos,
			values: []string{"main", "example-org/payments", "Run unit tests"},
			want:   "https://github.com/example-org/payments",
		},
		{
			name:   "the same repository written two ways is one repository",
			repos:  repos,
			values: []string{"example-org/payments", "https://github.com/example-org/payments.git", "github.com/example-org/payments"},
			want:   "https://github.com/example-org/payments",
		},
		{
			name:   "a sibling repository does not match on a shared prefix",
			repos:  repos,
			values: []string{"example-org/payments-u"},
			want:   "",
		},
		{
			name:   "two different repositories named leaves it undecided",
			repos:  repos,
			values: []string{"example-org/payments", "example-org/shared-lib"},
			want:   "",
		},
		{
			name:   "the same path on another host is another repository",
			repos:  repos,
			values: []string{"https://example.com/example-org/payments"},
			want:   "",
		},
		{
			name:   "a host-less value that fits repositories on two hosts is undecided",
			repos:  twoHosts,
			values: []string{"example-org/payments"},
			want:   "",
		},
		{
			name:   "a value with a host picks the repository on that host",
			repos:  twoHosts,
			values: []string{"https://gitlab.example.com/example-org/payments"},
			want:   "https://gitlab.example.com/example-org/payments",
		},
		{
			// A frame in a log line says where the frame lives, not which
			// repository the event is about.
			name:   "a module path inside a stack trace is not a naming",
			repos:  repos,
			values: []string{"panic: runtime error\n\tgithub.com/example-org/shared-lib/pkg/retry.go:42 +0x1f"},
			want:   "",
		},
		{
			name:   "a link into the repository is not a naming",
			repos:  repos,
			values: []string{"https://github.com/example-org/payments/actions/runs/1/job/2"},
			want:   "",
		},
		{
			name:   "a repository outside the integrations is ignored",
			repos:  repos,
			values: []string{"https://github.com/upstream-org/collector"},
			want:   "",
		},
		{
			name:   "no integration repositories",
			repos:  nil,
			values: []string{"example-org/payments"},
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, repoNamedBy(tt.repos, tt.values))
		})
	}
}
