package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runWithFakeCLIs runs command through /bin/sh with psql/mysql/sqlcmd/sqlplus
// replaced by scripts that print each argument on its own line between
// markers, and returns the arguments the CLI received.
func runWithFakeCLIs(t *testing.T, dir, command string) []string {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(bin, 0o755))
	for _, name := range []string{"psql", "mysql", "sqlcmd", "sqlplus"} {
		script := "#!/bin/sh\nfor a in \"$@\"; do printf '<%s>' \"$a\"; done\n"
		require.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755))
	}
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "PATH="+bin+":/usr/bin:/bin")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.Split(strings.TrimSuffix(strings.TrimPrefix(string(out), "<"), ">"), "><")
}

// The workspace pod runs these commands through a shell. Shell text inside a
// query must reach the database CLI as part of the query and never run in the
// pod.
func TestDBWorkspaceShellCommandsKeepQueryAsOneArgument(t *testing.T) {
	queries := []string{
		`select '$(touch pwned)' as a`,
		"select `touch pwned` as a",
		`select "Name", '$HOME' from t where x = 'it''s'`,
		"select 1\nfrom t; -- \" ' $(touch pwned)",
		`select * from t where a = "x" or b = '\'`,
	}
	for _, q := range queries {
		dir := t.TempDir()
		marker := filepath.Join(dir, "pwned")

		args := runWithFakeCLIs(t, dir, postgresWorkspaceShellCommand("--dbname 'db'", q))
		assert.Equal(t, []string{"--dbname", "db", "-c", q}, args, "psql")

		args = runWithFakeCLIs(t, dir, mssqlWorkspaceShellCommand(`d"b$(x)`, q))
		assert.Equal(t, []string{"-d", `d"b$(x)`, "-Q", q, "-s", "\t", "-W"}, args, "sqlcmd")

		args = runWithFakeCLIs(t, dir, oracleWorkspaceShellCommand("", q))
		assert.Equal(t, []string{"-Q", q}, args, "sqlplus")

		cmd, err := mysqlWorkspaceShellCommand(q)
		require.NoError(t, err)
		args = runWithFakeCLIs(t, dir, cmd)
		assert.Equal(t, []string{"-e", strings.TrimSpace(q)}, args, "mysql")

		_, statErr := os.Stat(marker)
		assert.True(t, os.IsNotExist(statErr), "shell text in %q ran in the workspace", q)
	}
}

// A full mysql command is accepted, word by word, without letting shell text
// in it run.
func TestMysqlWorkspaceShellCommandAcceptsFullCommand(t *testing.T) {
	dir := t.TempDir()
	cmd, err := mysqlWorkspaceShellCommand(`mysql -e "SHOW PROCESSLIST; $(touch pwned)"`)
	require.NoError(t, err)
	args := runWithFakeCLIs(t, dir, cmd)
	assert.Equal(t, []string{"-e", "SHOW PROCESSLIST; $(touch pwned)"}, args)
	_, statErr := os.Stat(filepath.Join(dir, "pwned"))
	assert.True(t, os.IsNotExist(statErr))

	_, err = mysqlWorkspaceShellCommand(`mysql -e "unclosed`)
	assert.Error(t, err)
}
