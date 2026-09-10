package handlers

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// relayReply wraps inner in the agent's reply envelope: the payload is a JSON
// string nested inside another JSON string under evidence[0].data.
func relayReply(inner string) []byte {
	innerJSON, _ := json.Marshal(inner)
	evidence, _ := json.Marshal(`[{"data":` + string(innerJSON) + `}]`)
	return []byte(`{"data":{"success":true,"findings":[{"evidence":[{"data":` + string(evidence) + `}]}]}}`)
}

func TestProcessRelayResponsePayload(t *testing.T) {
	tests := []struct {
		name        string
		inner       string
		requestType string
		want        string
	}{
		{
			name:        "query_range zips timestamps and values into samples",
			inner:       `{"query":{"series_list_result":[{"metric":{"job":"kubelet"},"timestamps":[1700000000,1700000060],"values":["1","2"]}]}}`,
			requestType: "query_range",
			want:        `{"data":{"result":[{"metric":{"job":"kubelet"},"values":[[1700000000,"1"],[1700000060,"2"]]}],"resultType":"matrix"},"stats":{},"status":"success"}`,
		},
		{
			name:        "query_range renders string timestamps as numbers",
			inner:       `{"query":{"series_list_result":[{"metric":{},"timestamps":["1700000000","1700000060.5"],"values":["1","2"]}]}}`,
			requestType: "query_range",
			want:        `{"data":{"result":[{"metric":{},"values":[[1700000000,"1"],[1700000060.5,"2"]]}],"resultType":"matrix"},"stats":{},"status":"success"}`,
		},
		{
			name:        "query_range uses exponent form for out-of-range timestamps",
			inner:       `{"query":{"series_list_result":[{"metric":{},"timestamps":[1e-9,1e22,0],"values":["a","b","c"]}]}}`,
			requestType: "query_range",
			want:        `{"data":{"result":[{"metric":{},"values":[[1e-9,"a"],[1e+22,"b"],[0,"c"]]}],"resultType":"matrix"},"stats":{},"status":"success"}`,
		},
		{
			name:        "query_range keeps unrecognised series fields",
			inner:       `{"query":{"series_list_result":[{"metric":{},"note":"kept","timestamps":[1],"values":["a"]}]}}`,
			requestType: "query_range",
			want:        `{"data":{"result":[{"metric":{},"note":"kept","values":[[1,"a"]]}],"resultType":"matrix"},"stats":{},"status":"success"}`,
		},
		{
			// Preserved from the previous implementation, which built the list
			// with append onto a nil slice.
			name:        "query_range renders an empty series list as null",
			inner:       `{"query":{"series_list_result":[]}}`,
			requestType: "query_range",
			want:        `{"data":{"result":null,"resultType":"matrix"},"stats":{},"status":"success"}`,
		},
		{
			name:        "query_range renders a null series list as null",
			inner:       `{"query":{"series_list_result":null}}`,
			requestType: "query_range",
			want:        `{"data":{"result":null,"resultType":"matrix"},"stats":{},"status":"success"}`,
		},
		{
			name:        "query_range renders a series with no samples as null",
			inner:       `{"query":{"series_list_result":[{"metric":{"a":"b"},"timestamps":[],"values":[]}]}}`,
			requestType: "query_range",
			want:        `{"data":{"result":[{"metric":{"a":"b"},"values":null}],"resultType":"matrix"},"stats":{},"status":"success"}`,
		},
		{
			name:        "query passes the vector result through",
			inner:       `{"query":{"result_type":"vector","vector_result":[{"metric":{"a":"b"},"value":[1,"2"]}]}}`,
			requestType: "query",
			want:        `{"data":{"result":{"result_type":"vector","vector_result":[{"metric":{"a":"b"},"value":[1,"2"]}]},"resultType":"vector"},"stats":{},"status":"success"}`,
		},
		{
			name:        "query renders a missing result as null",
			inner:       `{"notquery":1}`,
			requestType: "query",
			want:        `{"data":{"result":null,"resultType":"vector"},"stats":{},"status":"success"}`,
		},
		{
			name:        "labels passes the data field through",
			inner:       `{"data":["a","b"]}`,
			requestType: "labels",
			want:        `{"data":["a","b"],"stats":{},"status":"success"}`,
		},
		{
			name:        "labels renders a missing data field as null",
			inner:       `{"nodata":1}`,
			requestType: "labels",
			want:        `{"data":null,"stats":{},"status":"success"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := processRelayResponsePayload(relayReply(tt.inner), tt.requestType)
			require.NoError(t, err)
			assert.JSONEq(t, tt.want, string(got))
			assert.Equal(t, tt.want, string(got), "output should be byte-for-byte stable")
		})
	}
}

func TestProcessRelayResponsePayloadErrors(t *testing.T) {
	tests := []struct {
		name        string
		reply       []byte
		requestType string
		wantErr     string
	}{
		{
			name:        "agent reported failure",
			reply:       []byte(`{"data":{"success":false}}`),
			requestType: "query_range",
			wantErr:     "relay: unable to execute relay query",
		},
		{
			name:        "evidence missing",
			reply:       []byte(`{"data":{"success":true,"findings":[]}}`),
			requestType: "query_range",
			wantErr:     "relay: unable to execute relay query",
		},
		{
			name:        "series_list_result missing",
			reply:       relayReply(`{"query":{}}`),
			requestType: "query_range",
			wantErr:     "series_list_result not found",
		},
		{
			name:        "series_list_result not an array",
			reply:       relayReply(`{"query":{"series_list_result":{"a":1}}}`),
			requestType: "query_range",
			wantErr:     "failed to parse series_list_result: not an array",
		},
		{
			name:        "series is not an object",
			reply:       relayReply(`{"query":{"series_list_result":["nope"]}}`),
			requestType: "query_range",
			wantErr:     "invalid series item format",
		},
		{
			name:        "timestamps missing",
			reply:       relayReply(`{"query":{"series_list_result":[{"metric":{},"values":["a"]}]}}`),
			requestType: "query_range",
			wantErr:     "missing or invalid 'timestamp' or 'values' in series",
		},
		{
			name:        "more timestamps than values",
			reply:       relayReply(`{"query":{"series_list_result":[{"metric":{},"timestamps":[1,2],"values":["a"]}]}}`),
			requestType: "query_range",
			wantErr:     "mismatch between number of timestamps and values",
		},
		{
			name:        "more values than timestamps",
			reply:       relayReply(`{"query":{"series_list_result":[{"metric":{},"timestamps":[1],"values":["a","b"]}]}}`),
			requestType: "query_range",
			wantErr:     "mismatch between number of timestamps and values",
		},
		{
			name:        "timestamp is neither number nor string",
			reply:       relayReply(`{"query":{"series_list_result":[{"metric":{},"timestamps":[true],"values":["a"]}]}}`),
			requestType: "query_range",
			wantErr:     "timestamp is not a string or float64",
		},
		{
			name:        "timestamp string does not parse",
			reply:       relayReply(`{"query":{"series_list_result":[{"metric":{},"timestamps":["abc"],"values":["a"]}]}}`),
			requestType: "query_range",
			wantErr:     "could not parse timestamp string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := processRelayResponsePayload(tt.reply, tt.requestType)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Nil(t, got)
		})
	}
}
