package core

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every combination of the three switches: 3 states (unset/true/false) ^ 3 = 27.
//
// The expected values come from the one rule the resolvers commit to, written
// out independently of the implementation so this checks rather than restates:
// reading gates the other two, and an unset switch allows.
//
// Deployment, tenant enrolment and user consent are deliberately not modelled
// here — they are each gate's own check, made before it asks the request.
func TestMemoryOverrides_FullMatrix(t *testing.T) {
	states := []*bool{nil, boolPtr(true), boolPtr(false)}
	name := func(p *bool) string {
		if p == nil {
			return "unset"
		}
		return fmt.Sprintf("%t", *p)
	}
	on := func(sw *bool) bool { return sw == nil || *sw }

	cases := 0
	for _, read := range states {
		for _, write := range states {
			for _, rerank := range states {
				cases++
				q := NBQueryConfig{Memory: &MemoryOverrides{Read: read, Write: write, Rerank: rerank}}
				wantRead := on(read)
				t.Run(fmt.Sprintf("read=%s/write=%s/rerank=%s", name(read), name(write), name(rerank)), func(t *testing.T) {
					assert.Equal(t, wantRead, q.MemoryReadAllowed(), "read")
					assert.Equal(t, wantRead && on(write), q.MemoryWriteAllowed(), "write")
					assert.Equal(t, wantRead && on(rerank), q.MemoryRerankAllowed(), "rerank")
				})
			}
		}
	}
	require.Equal(t, 27, cases, "the matrix must stay exhaustive if a state is added")
}

// A nil block must behave exactly as an all-unset block, or "no override" and
// "empty override" diverge.
func TestMemoryOverrides_NilBlockMatchesAllUnset(t *testing.T) {
	none := NBQueryConfig{}
	empty := NBQueryConfig{Memory: &MemoryOverrides{}}
	assert.Equal(t, none.MemoryReadAllowed(), empty.MemoryReadAllowed())
	assert.Equal(t, none.MemoryWriteAllowed(), empty.MemoryWriteAllowed())
	assert.Equal(t, none.MemoryRerankAllowed(), empty.MemoryRerankAllowed())
}

// Every combination must survive JSON, since callers send it over the wire and
// an absent key must not become false.
func TestMemoryOverrides_JSONMatrix(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"memory":{}}`,
		`{"memory":{"read":false}}`,
		`{"memory":{"write":false}}`,
		`{"memory":{"rerank":false}}`,
		`{"memory":{"write":false,"rerank":false}}`,
		`{"memory":{"read":false,"write":false,"rerank":false}}`,
		`{"memory":{"read":true,"write":true,"rerank":true}}`,
	} {
		t.Run(body, func(t *testing.T) {
			var q NBQueryConfig
			require.NoError(t, json.Unmarshal([]byte(body), &q))
			out, err := json.Marshal(q)
			require.NoError(t, err)
			var back NBQueryConfig
			require.NoError(t, json.Unmarshal(out, &back))
			assert.Equal(t, q.MemoryReadAllowed(), back.MemoryReadAllowed())
			assert.Equal(t, q.MemoryWriteAllowed(), back.MemoryWriteAllowed())
			assert.Equal(t, q.MemoryRerankAllowed(), back.MemoryRerankAllowed())
		})
	}
}
