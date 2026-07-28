package handlers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildWorkspaceAction_DbNameSanitization(t *testing.T) {
	cases := []struct {
		name     string
		tool     string
		dbName   string
		notInCmd string
	}{
		{"postgres injection", "psql", `mydb"; rm -rf /; echo "`, `rm -rf`},
		{"mysql injection", "mysql", "mydb; cat /etc/passwd", "cat /etc/passwd"},
		{"clickhouse injection", "clickhouse", "db$(whoami)", "$(whoami)"},
		{"postgres backtick", "psql", "db`id`", "`id`"},
		{"mysql semicolon", "mysql", "db;ls", ";ls"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"database": tc.dbName}
			_, params, err := buildWorkspaceAction(tc.tool, "SELECT 1", nil, args, "shell:latest")
			assert.NoError(t, err)
			cmd, _ := params["command"].(string)
			assert.NotContains(t, cmd, tc.notInCmd,
				"sanitizeNameRe must strip dangerous characters from database name")
		})
	}
}

func TestBuildWorkspaceAction_DbNameAllUnsafe(t *testing.T) {
	cases := []struct {
		name string
		tool string
	}{
		{"postgres", "psql"},
		{"mysql", "mysql"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"database": ";!@#"}
			_, params, err := buildWorkspaceAction(tc.tool, "SELECT 1", nil, args, "shell:latest")
			assert.NoError(t, err)
			cmd, _ := params["command"].(string)
			assert.NotContains(t, cmd, "--dbname ", "empty sanitized name must not produce a dangling --dbname flag")
			assert.NotContains(t, cmd, "--database=", "empty sanitized name must not produce a dangling --database flag")
		})
	}
}
