package egressfilter

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactCommandLine(t *testing.T) {
	for _, tc := range []struct {
		command string
		secret  string
		keep    string
	}{
		{`mysql -u root --password hunter2 -h db`, "hunter2", "mysql -u root --password"},
		{`mysql -u root -p hunter2 -h db`, "hunter2", "-h db"},
		{`mysql -u root -phunter2 -h db`, "hunter2", "-h db"},
		{`psql -h db -U app -p hunter2`, "hunter2", "psql -h db -U app"},
		{`gh api --token=abc123secret /repos`, "abc123secret", "/repos"},
		{`gh api --token abc123secret /repos`, "abc123secret", "/repos"},
		{`argocd login cd.example.com --auth-token 'tok en'`, "tok en", "argocd login cd.example.com"},
		{`git clone https://user:pass123@github.com/x/y`, "pass123", "https://user:"},
		{`curl -H "Authorization: Bearer abc123token" https://api.example.com`, "abc123token", "https://api.example.com"},
		{`curl -H 'X-Api-Key: k3y' https://api.example.com`, "k3y", "https://api.example.com"},
		{`AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY aws s3 ls`, "wJalrXUtnFEMIK7MDENGbPxRfiCYEXAMPLEKEY", "aws s3 ls"},
		{`PGPASSWORD=x psql -h db`, "PGPASSWORD=x ", "psql -h db"},
		{`export GITHUB_TOKEN="ghp_abc"; gh pr list`, "ghp_abc", "gh pr list"},
		{`redis-cli -h cache -a s3cret INFO`, "s3cret", "INFO"},
		{`{"command":"PGPASSWORD=x psql -h db -p hunter2"}`, "hunter2", `{"command":"PGPASSWORD=`},
		{`sh -c 'MYSQL_PWD=s3cret mysql -u root'`, "s3cret", "mysql -u root"},
	} {
		got := RedactCommandLine(tc.command)
		assert.NotContains(t, got, tc.secret, tc.command)
		assert.Contains(t, got, commandLinePlaceholder, tc.command)
		assert.Contains(t, got, tc.keep, "the rest of the command stays readable: %s", tc.command)
	}
}

func TestRedactCommandLineLeavesPlainCommands(t *testing.T) {
	for _, command := range []string{
		`kubectl rollout restart deploy/api -n shop`,
		`kubectl get pods -o jsonpath='{.items[*].metadata.name}'`,
		`mysql -u root -h db -e 'select 1'`,
		`helm upgrade api ./chart --set image.tag=1.2.3`,
		`curl -X POST https://api.example.com/hooks`,
		``,
	} {
		assert.Equal(t, command, RedactCommandLine(command))
	}
	assert.False(t, strings.Contains(RedactCommandLine(`psql --password -h db`), commandLinePlaceholder), "a flag followed by another flag has no value to mask")
}
