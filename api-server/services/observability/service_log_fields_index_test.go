package observability

import (
	"testing"
)

// TestResolveLogFieldsIndex pins request.index > account default, and that
// "neither" resolves to "" rather than a wildcard. The empty case is the
// load-bearing one: the ES sources hold a second default (cfg.LogIndex) that
// only they can see, so widening to "*" here would mask an index the account
// actually configured — which is exactly what it did when this returned "*".
func TestResolveLogFieldsIndex(t *testing.T) {
	tests := []struct {
		name         string
		requestIndex string
		defaultIndex string
		want         string
	}{
		{
			name:         "explicit request index wins over the account default",
			requestIndex: "logs-app-*",
			defaultIndex: "logs-default-*",
			want:         "logs-app-*",
		},
		{
			name:         "falls back to the account default when no index is requested",
			defaultIndex: "logs-default-*",
			want:         "logs-default-*",
		},
		{
			name:         "whitespace-only request index is not an index",
			requestIndex: "   ",
			defaultIndex: "logs-default-*",
			want:         "logs-default-*",
		},
		{
			name:         "trims a padded index rather than passing it to the URL",
			requestIndex: "  logs-app-*  ",
			want:         "logs-app-*",
		},
		{
			name:         "whitespace-only default resolves to nothing, not a wildcard",
			defaultIndex: "  ",
			want:         "",
		},
		{
			name: "neither resolved defers to the source rather than widening here",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveLogFieldsIndex(tt.requestIndex, tt.defaultIndex); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
