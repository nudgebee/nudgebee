package tools

import "testing"

func TestParseWindowDays(t *testing.T) {
	tests := []struct {
		name    string
		window  string
		want    int
		wantErr bool
	}{
		{name: "empty defaults to 30", window: "", want: 30},
		{name: "7d", window: "7d", want: 7},
		{name: "15d arbitrary", window: "15d", want: 15},
		{name: "30d", window: "30d", want: 30},
		{name: "90d", window: "90d", want: 90},
		{name: "45d arbitrary", window: "45d", want: 45},
		{name: "1d lower bound", window: "1d", want: 1},
		{name: "365d upper bound", window: "365d", want: 365},
		{name: "0d below bound", window: "0d", wantErr: true},
		{name: "366d above bound", window: "366d", wantErr: true},
		{name: "negative", window: "-5d", wantErr: true},
		{name: "missing suffix", window: "30", wantErr: true},
		{name: "non-numeric", window: "abcd", wantErr: true},
		{name: "wrong unit", window: "30h", wantErr: true},
		{name: "float", window: "1.5d", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWindowDays(tt.window)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseWindowDays(%q): expected error, got nil (days=%d)", tt.window, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWindowDays(%q): unexpected error: %v", tt.window, err)
			}
			if got != tt.want {
				t.Errorf("parseWindowDays(%q) = %d, want %d", tt.window, got, tt.want)
			}
		})
	}
}
