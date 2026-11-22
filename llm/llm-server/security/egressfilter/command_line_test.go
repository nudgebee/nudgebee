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
		{`mysql -p'hunter2' db`, "hunter2", " db"},
		{`mysql -p"hunter 2" db`, "hunter 2", " db"},
		{`mysql -u root -p"Hunter2"`, "Hunter2", "mysql -u root -p"},
		{`mariadb-dump -u root -p'hunter2' shop`, "hunter2", " shop"},
		{`psql -h db -U app --password hunter2`, "hunter2", "psql -h db -U app --password"},
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
		{`{"command":"PGPASSWORD=hunter2 psql -h db -p 5432"}`, "hunter2", `psql -h db -p 5432"}`},
		{`sh -c 'MYSQL_PWD=s3cret mysql -u root'`, "s3cret", "mysql -u root"},
		{`curl -u admin:s3cret https://api.example.com`, "s3cret", "curl -u admin:"},
		{`curl -u admin:'s3cr!t' https://api.example.com`, "s3cr!t", "curl -u admin:"},
		{`curl -u admin:"s3cret" https://api.example.com`, "s3cret", " https://api.example.com"},
		{`curl -uadmin:s3cret https://api.example.com`, "s3cret", "curl -uadmin:"},
		{`curl --user=admin:s3cret https://api.example.com`, "s3cret", "https://api.example.com"},
		// The workspace relays each argument quoted.
		{`kubectl 'get' 'pods' '--token' 's3cret'`, "s3cret", `kubectl 'get' 'pods' '--token'`},
		{`kubectl 'get' 'pods' '--token=s3cret'`, "s3cret", `'--token=`},
		{`kubectl 'exec' 'db-0' '--' 'mysql' '-uroot' '-ps3cret'`, "s3cret", `'mysql' '-uroot' '-p`},
		{`kubectl 'exec' 'db-0' '--' 'mysql' '-h' 'db' '-p' 's3cret'`, "s3cret", `'mysql' '-h' 'db' '-p'`},
		{`kubectl 'exec' 'db-0' '--' 'mysql' '-uroot' '-p's3cret''`, "s3cret", `'mysql' '-uroot'`},
		{`kubectl 'exec' 'db-0' '--' 'psql' '-p' '5432' 'PGPASSWORD=s3cret'`, "s3cret", `'psql' '-p' '5432'`},
		{`kubectl 'exec' 'cache-0' '--' 'redis-cli' '-a' 's3cret' 'INFO'`, "s3cret", `'INFO'`},
		{`kubectl 'exec' 'x' '--' 'curl' '-u' 'admin:s3cret' 'https://api.example.com'`, "s3cret", `'curl' '-u' 'admin:`},
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
		`curl -u admin https://api.example.com`,
		`kubectl 'rollout' 'restart' 'deploy/api' '-n' 'shop'`,
		``,
	} {
		assert.Equal(t, command, RedactCommandLine(command))
	}
	assert.False(t, strings.Contains(RedactCommandLine(`psql --password -h db`), commandLinePlaceholder), "a flag followed by another flag has no value to mask")
	assert.False(t, strings.Contains(RedactCommandLine(`kubectl 'exec' 'db-0' '--' 'psql' '--password' '-h' 'db'`), commandLinePlaceholder), "a quoted flag followed by another flag has no value to mask")
	assert.False(t, strings.Contains(RedactCommandLine(`kubectl 'exec' 'db-0' '--' 'mysql' '-p' '-h' 'db'`), commandLinePlaceholder), "the gap between quoted arguments is not a quoted password")
}

// For the psql family -p is the port, not the password.
func TestRedactCommandLineKeepsPsqlPort(t *testing.T) {
	for _, command := range []string{
		`psql -p 5432 -h db.prod -U app -c 'select 1'`,
		`psql -h db -p5432 app`,
		`pg_dump -h db -p 5433 -U app shop`,
		`pg_dumpall -p 5432 -h db`,
		`pg_restore -p 5432 -d shop dump.sql`,
		`kubectl 'exec' 'db-0' '--' 'psql' '-p' '5432' '-h' 'db'`,
	} {
		assert.Equal(t, command, RedactCommandLine(command))
	}
}
