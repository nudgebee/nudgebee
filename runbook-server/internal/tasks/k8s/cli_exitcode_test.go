package k8s

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestInterpretKubectlResponse covers the silent-failure fix: any non-zero
// kubectl exit must surface as a task error, even when stderr doesn't match the
// legacy "error:"/"fail" substring heuristic (the case that let Forbidden /
// NotFound / verb-rejected writes pass as success).
func TestInterpretKubectlResponse(t *testing.T) {
	cases := []struct {
		name      string
		resp      string
		expectErr bool
		errSub    string
		data      string
	}{
		{
			name: "success, exit 0",
			resp: `{"stdout":"deployment.apps/api scaled","stderr":"","exit_code":0}`,
			data: "scaled",
		},
		{
			name:      "non-zero exit with Forbidden (no 'error:'/'fail' substring) surfaces",
			resp:      `{"stdout":"","stderr":"Error from server (Forbidden): deployments.apps \"api\" is forbidden","exit_code":1}`,
			expectErr: true,
			errSub:    "exited 1",
		},
		{
			name:      "non-zero exit with 'no matches for kind' surfaces (the heuristic gap this fixes)",
			resp:      `{"stdout":"","stderr":"the server doesn't have a resource type","exit_code":1}`,
			expectErr: true,
			errSub:    "exited 1",
		},
		{
			name:      "exit 0 but stderr 'error:' still errors (fallback heuristic retained)",
			resp:      `{"stdout":"","stderr":"error: something bad","exit_code":0}`,
			expectErr: true,
		},
		{
			name:      "non-zero exit with empty output falls back to a generic message",
			resp:      `{"stdout":"","stderr":"","exit_code":1}`,
			expectErr: true,
			errSub:    "unknown error",
		},
		{
			name: "success carries a non-error stderr warning through as data",
			resp: `{"stdout":"NAME\napi","stderr":"Warning: v1 is deprecated","exit_code":0}`,
			data: "NAME",
		},
		{
			name:      "no exit_code present, falls back to stderr heuristic (error)",
			resp:      `{"stdout":"","stderr":"failed to connect to cluster"}`,
			expectErr: true,
		},
		{
			name: "no exit_code present, clean stderr, succeeds",
			resp: `{"stdout":"ok","stderr":""}`,
			data: "ok",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := interpretKubectlResponse(tc.resp)
			if tc.expectErr {
				assert.Error(t, err)
				assert.Nil(t, out)
				if tc.errSub != "" {
					assert.Contains(t, err.Error(), tc.errSub)
				}
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, out)
			assert.Contains(t, out["data"].(string), tc.data)
		})
	}
}

func TestKubectlExitCode(t *testing.T) {
	for _, c := range []struct {
		in   any
		want int
		ok   bool
	}{
		{float64(0), 0, true},
		{float64(1), 1, true},
		{float32(4), 4, true},
		{float64(-1), -1, true}, // process never started
		{2, 2, true},
		{int32(5), 5, true},
		{int64(3), 3, true},
		{nil, 0, false},
		{"1", 0, false}, // strings are not exit codes
	} {
		got, ok := kubectlExitCode(c.in)
		assert.Equal(t, c.ok, ok)
		if ok {
			assert.Equal(t, c.want, got)
		}
	}
}
