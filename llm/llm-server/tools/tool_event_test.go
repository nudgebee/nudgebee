package tools

import (
	"encoding/json"
	"nudgebee/llm/events"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestEventAgentExecuteTool(t *testing.T) {

	tool := EventsExecuteTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-1",
				Query:     `select * from events where id = '050ef726-d4e0-4c21-960b-5ee928e642f8'`,
				AccountId: "e9dc2b39-78d3-46da-9691-f160bd3c4c19",
				UserId:    "924f41d7-21bd-4ae5-ba4d-3f4abd4f37a9",
			},
		}
	for _, tc := range testCases {

		ntc := core.NewNbToolContext(sc, tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		var event []events.Event
		err = json.Unmarshal([]byte(resp.Data), &event)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestEventAgentExecuteTool2(t *testing.T) {

	tool := EventsExecuteTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-1",
				Query:     `select * from events where id = '983897e7-9afb-48c5-bd27-83a8784e5f2d'`,
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
			},
		}
	for _, tc := range testCases {

		ntc := core.NewNbToolContext(sc, tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		var event []events.Event
		err = json.Unmarshal([]byte(resp.Data), &event)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestEventAgentExecuteCloudAccount(t *testing.T) {

	tool := EventsExecuteTool{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-prom-chain-1",
				Query:     `select * from events where id = '61184c30-225e-45f7-acd5-63e2f83fbcad'`,
				AccountId: "49145907-981b-48ad-a67a-08a4e8099cb2",
				UserId:    os.Getenv("TEST_USER"),
			},
		}
	for _, tc := range testCases {

		ntc := core.NewNbToolContext(sc, tool, tc.AccountId, tc.UserId, uuid.NewString(), uuid.NewString(), uuid.NewString(), tc.Query, []llms.MessageContent{}, "", core.NBQueryConfig{}, "")
		resp, err := tool.Call(ntc, core.NBToolCallRequest{
			Command: tc.Query,
		})
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		var event []events.Event
		err = json.Unmarshal([]byte(resp.Data), &event)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}
}

func TestCastIsoTimestampLiterals(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "timestamp minus interval gets cast (the 22007 bug)",
			in:   "SELECT * FROM events WHERE starts_at >= '2026-06-23T08:31:42.807795Z' - INTERVAL '1 hour'",
			want: "SELECT * FROM events WHERE starts_at >= '2026-06-23T08:31:42.807795Z'::timestamp - INTERVAL '1 hour'",
		},
		{
			name: "both bounds of a window are cast",
			in:   "WHERE starts_at >= '2026-06-23T08:31:42Z' - INTERVAL '1 hour' AND starts_at <= '2026-06-23T08:31:42Z' + INTERVAL '1 hour'",
			want: "WHERE starts_at >= '2026-06-23T08:31:42Z'::timestamp - INTERVAL '1 hour' AND starts_at <= '2026-06-23T08:31:42Z'::timestamp + INTERVAL '1 hour'",
		},
		{
			name: "plain comparison literal still cast (harmless, valid)",
			in:   "WHERE starts_at = '2025-01-25 13:00:00'",
			want: "WHERE starts_at = '2025-01-25 13:00:00'::timestamp",
		},
		{
			name: "already-cast literal is not double-cast",
			in:   "WHERE starts_at >= '2026-06-23T08:31:42Z'::timestamp - INTERVAL '1 hour'",
			want: "WHERE starts_at >= '2026-06-23T08:31:42Z'::timestamp - INTERVAL '1 hour'",
		},
		{
			name: "interval duration literal is untouched",
			in:   "WHERE starts_at >= NOW() - INTERVAL '24 hours'",
			want: "WHERE starts_at >= NOW() - INTERVAL '24 hours'",
		},
		{
			name: "timestamp-shaped value right after INTERVAL keyword is left alone",
			in:   "WHERE starts_at >= NOW() - INTERVAL '2026-06-23 01:00:00'",
			want: "WHERE starts_at >= NOW() - INTERVAL '2026-06-23 01:00:00'",
		},
		{
			name: "2-digit timezone offset is matched and cast",
			in:   "WHERE starts_at >= '2026-06-23T08:31:42+02' - INTERVAL '1 hour'",
			want: "WHERE starts_at >= '2026-06-23T08:31:42+02'::timestamp - INTERVAL '1 hour'",
		},
		{
			name: "identifier ending in interval is not mistaken for the keyword",
			in:   "WHERE custom_interval '2026-06-23T08:31:42Z'",
			want: "WHERE custom_interval '2026-06-23T08:31:42Z'::timestamp",
		},
		{
			name: "date-only literal is untouched (no time component)",
			in:   "WHERE starts_at >= '2026-06-23'",
			want: "WHERE starts_at >= '2026-06-23'",
		},
		{
			name: "no timestamp literal is a no-op",
			in:   "SELECT * FROM events WHERE priority = 'high' LIMIT 5",
			want: "SELECT * FROM events WHERE priority = 'high' LIMIT 5",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := castIsoTimestampLiterals(tc.in); got != tc.want {
				t.Errorf("castIsoTimestampLiterals()\n  in:   %s\n  got:  %s\n  want: %s", tc.in, got, tc.want)
			}
		})
	}
}
