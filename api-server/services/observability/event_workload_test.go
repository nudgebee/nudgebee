package observability

import (
	"testing"

	"nudgebee/services/eventrule/playbooks"
)

func TestGetEventWorkload(t *testing.T) {
	tests := []struct {
		name  string
		event playbooks.PlaybookEvent
		want  string
	}{
		{
			name: "SubjectOwner present — preferred over SubjectName",
			event: playbooks.PlaybookEvent{
				SubjectName:  "checkout-66cbdd8d5-w6kxc",
				SubjectOwner: "checkout",
			},
			want: "checkout",
		},
		{
			name: "SubjectOwner empty — falls back to SubjectName",
			event: playbooks.PlaybookEvent{
				SubjectName:  "checkout-66cbdd8d5-w6kxc",
				SubjectOwner: "",
			},
			want: "checkout-66cbdd8d5-w6kxc",
		},
		{
			name: "both empty — returns empty",
			event: playbooks.PlaybookEvent{
				SubjectName:  "",
				SubjectOwner: "",
			},
			want: "",
		},
		{
			name: "only SubjectName set — typical pre-resolver event",
			event: playbooks.PlaybookEvent{
				SubjectName: "rabbitmq-0",
			},
			want: "rabbitmq-0",
		},
		{
			name: "only SubjectOwner set — agent direct-kind event",
			event: playbooks.PlaybookEvent{
				SubjectOwner: "fluent-bit",
			},
			want: "fluent-bit",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := getEventWorkload(tc.event); got != tc.want {
				t.Errorf("getEventWorkload(%+v) = %q; want %q", tc.event, got, tc.want)
			}
		})
	}
}
