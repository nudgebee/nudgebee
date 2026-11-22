package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"nudgebee/services/integrations"
)

func TestSplunkSignalFlowStopNote(t *testing.T) {
	points := []integrations.SignalFlowDataPoint{
		{TimestampMs: 1700000000000},
		{TimestampMs: 1700000060000},
	}
	tests := []struct {
		name   string
		stop   integrations.SignalFlowStop
		points []integrations.SignalFlowDataPoint
		want   string
	}{
		{"complete stream", "", points, ""},
		{
			"deadline uses newest point",
			integrations.SignalFlowStopDeadline,
			points,
			"Splunk stopped streaming this query after 60s, so the series end at 2023-11-14T22:14:20Z and the rest of the requested window is missing. Narrow the window or the program's filters.",
		},
		{
			"aborted",
			integrations.SignalFlowStopAborted,
			points[:1],
			"Splunk aborted the channel for this query, so the series end at 2023-11-14T22:13:20Z and the rest of the requested window is missing. Narrow the window or the program's filters.",
		},
		{
			"deadline with no points",
			integrations.SignalFlowStopDeadline,
			nil,
			"Splunk stopped streaming this query after 60s before any data arrived, so the requested window is missing. Narrow the window or the program's filters.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, splunkSignalFlowStopNote(tt.stop, tt.points))
		})
	}
}
