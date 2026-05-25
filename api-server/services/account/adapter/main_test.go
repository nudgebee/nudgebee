package adapter

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSafeFilePath(t *testing.T) {
	tests := []struct {
		name     string
		dir      string
		userPath string
		wantErr  bool
	}{
		{"valid relative path", "/tmp/repo", "values.yaml", false},
		{"valid nested path", "/tmp/repo", "charts/app/values.yaml", false},
		{"traversal with dotdot", "/tmp/repo", "../../etc/passwd", true},
		{"traversal mid-path", "/tmp/repo", "charts/../../etc/passwd", true},
		{"absolute path joined safely", "/tmp/repo", "/etc/passwd", false},
		{"dotdot only", "/tmp/repo", "..", true},
		{"current dir", "/tmp/repo", ".", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := safeFilePath(tt.dir, tt.userPath)
			if tt.wantErr {
				assert.Error(t, err, "expected error for userPath=%q", tt.userPath)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, result)
			}
		})
	}
}
