package tools

import "testing"

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

func TestInjectIdPredicate(t *testing.T) {
	const view = `select * from (SELECT id::text FROM events WHERE cloud_account_id = 'acct-1'::uuid) as e`
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{
			name:    "single id equality, spaced, gets pushed down typed",
			command: "select * from events where id = '812b3bb6-67ef-45de-a310-783c538cdb7e'",
			want:    `select * from (SELECT id::text FROM events WHERE id = '812b3bb6-67ef-45de-a310-783c538cdb7e'::uuid AND cloud_account_id = 'acct-1'::uuid) as e`,
		},
		{
			name:    "single id equality, no spaces, still matches",
			command: "select * from events where id='812b3bb6-67ef-45de-a310-783c538cdb7e'",
			want:    `select * from (SELECT id::text FROM events WHERE id = '812b3bb6-67ef-45de-a310-783c538cdb7e'::uuid AND cloud_account_id = 'acct-1'::uuid) as e`,
		},
		{
			name:    "case-insensitive ID keyword",
			command: "select * from events where ID = '812b3bb6-67ef-45de-a310-783c538cdb7e'",
			want:    `select * from (SELECT id::text FROM events WHERE id = '812b3bb6-67ef-45de-a310-783c538cdb7e'::uuid AND cloud_account_id = 'acct-1'::uuid) as e`,
		},
		{
			name:    "event_id column is not mistaken for id",
			command: "select * from events where event_id = '812b3bb6-67ef-45de-a310-783c538cdb7e'",
			want:    view,
		},
		{
			name:    "multiple ids (IN-list shaped) falls back unchanged, no unsafe single-id guess",
			command: "select * from events where id = '812b3bb6-67ef-45de-a310-783c538cdb7e' or id = '923c4cc7-78f0-56ef-b421-894649dee8f8'",
			want:    view,
		},
		{
			name:    "no id predicate at all is a no-op",
			command: "select * from events where priority = 'high' limit 5",
			want:    view,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := injectIdPredicate(view, tc.command); got != tc.want {
				t.Errorf("injectIdPredicate()\n  command: %s\n  got:     %s\n  want:    %s", tc.command, got, tc.want)
			}
		})
	}
}
