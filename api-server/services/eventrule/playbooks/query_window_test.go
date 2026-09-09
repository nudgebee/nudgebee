package playbooks

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func at(t time.Time) *time.Time { return &t }

func TestResolveQueryWindow(t *testing.T) {
	base := time.Date(2026, 8, 20, 8, 24, 45, 0, time.UTC)

	tests := []struct {
		name     string
		event    PlaybookEvent
		duration int
		want     time.Duration
		wantEnd  *time.Time
	}{
		{
			// Enrichment starts within seconds of the event row being written, so a
			// still-firing alert used to produce a window this narrow — too narrow for
			// any backend to return a sample or a log line.
			name:    "just-fired alert widens to the floor",
			event:   PlaybookEvent{StartedAt: at(base), EndedAt: at(base.Add(5 * time.Second))},
			want:    DefaultQueryWindowMinutes * time.Minute,
			wantEnd: at(base.Add(5 * time.Second)),
		},
		{
			name:    "wider event window is preserved",
			event:   PlaybookEvent{StartedAt: at(base), EndedAt: at(base.Add(3 * time.Hour))},
			want:    3 * time.Hour,
			wantEnd: at(base.Add(3 * time.Hour)),
		},
		{
			name:    "long-firing alert is capped",
			event:   PlaybookEvent{StartedAt: at(base.Add(-5 * 24 * time.Hour)), EndedAt: at(base)},
			want:    MaxQueryWindowMinutes * time.Minute,
			wantEnd: at(base),
		},
		{
			name:     "caller duration is the floor",
			event:    PlaybookEvent{StartedAt: at(base), EndedAt: at(base.Add(time.Second))},
			duration: 10,
			want:     10 * time.Minute,
			wantEnd:  at(base.Add(time.Second)),
		},
		{
			name:     "caller duration does not shrink a wider event window",
			event:    PlaybookEvent{StartedAt: at(base), EndedAt: at(base.Add(2 * time.Hour))},
			duration: 10,
			want:     2 * time.Hour,
			wantEnd:  at(base.Add(2 * time.Hour)),
		},
		{
			name:    "missing start uses the floor ending at EndedAt",
			event:   PlaybookEvent{EndedAt: at(base)},
			want:    DefaultQueryWindowMinutes * time.Minute,
			wantEnd: at(base),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			start, end := tc.event.ResolveQueryWindow(tc.duration)
			assert.Equal(t, tc.want, end.Sub(start), "window width")
			if tc.wantEnd != nil {
				assert.True(t, tc.wantEnd.Equal(end), "want end %s, got %s", tc.wantEnd, end)
			}
		})
	}
}

func TestResolveQueryWindowStillFiring(t *testing.T) {
	// EndedAt nil means the alert has not resolved: the window ends now.
	start, end := PlaybookEvent{StartedAt: at(time.Now())}.ResolveQueryWindow(0)
	assert.Equal(t, DefaultQueryWindowMinutes*time.Minute, end.Sub(start))
	assert.WithinDuration(t, time.Now(), end, 5*time.Second)
}

func TestResolveQueryWindowNoTimes(t *testing.T) {
	start, end := PlaybookEvent{}.ResolveQueryWindow(0)
	assert.Equal(t, DefaultQueryWindowMinutes*time.Minute, end.Sub(start))
	assert.WithinDuration(t, time.Now(), end, 5*time.Second)
}

func TestDecodeAgentPayload(t *testing.T) {
	const twoLines = `b'H4sIAAAAAAAA/youSSwqycxL50pLLEnMsVJITszLyy9RSM7Py0tNLlEoyVdISeICBAAA//9rwZaxJQAAAA=='`

	t.Run("unwraps bytes-repr gzip", func(t *testing.T) {
		got, err := DecodeAgentPayload(twoLines)
		assert.NoError(t, err)
		assert.Equal(t, "starting\nfatal: cannot connect to db\n", got)
	})

	t.Run("plain text is returned unchanged", func(t *testing.T) {
		got, err := DecodeAgentPayload("plain output")
		assert.NoError(t, err)
		assert.Equal(t, "plain output", got)
	})

	t.Run("malformed wrapper errors", func(t *testing.T) {
		_, err := DecodeAgentPayload("b'@@@not-base64@@@'")
		assert.Error(t, err)
	})

	t.Run("IsWrappedAgentPayload", func(t *testing.T) {
		assert.True(t, IsWrappedAgentPayload(twoLines))
		assert.True(t, IsWrappedAgentPayload(`b"H4sI"`))
		assert.False(t, IsWrappedAgentPayload("plain output"))
	})
}

func TestDecodeAgentPayloadRejectsDecompressionBomb(t *testing.T) {
	// The agent compresses a pod's own stdout, so a workload stuck in a log loop
	// produces this shape with no attacker involved.
	bomb := func(size int) string {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, err := zw.Write(make([]byte, size))
		require.NoError(t, err)
		require.NoError(t, zw.Close())
		return "b'" + base64.StdEncoding.EncodeToString(buf.Bytes()) + "'"
	}

	t.Run("over the cap is refused, not truncated", func(t *testing.T) {
		payload := bomb(maxDecodedPayloadBytes + 1024)
		assert.Less(t, len(payload), 200_000, "a small blob should expand past the cap")

		got, err := DecodeAgentPayload(payload)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds")
		assert.Empty(t, got)
	})

	t.Run("a payload at the cap still decodes", func(t *testing.T) {
		got, err := DecodeAgentPayload(bomb(maxDecodedPayloadBytes))
		require.NoError(t, err)
		assert.Len(t, got, maxDecodedPayloadBytes)
	})
}
