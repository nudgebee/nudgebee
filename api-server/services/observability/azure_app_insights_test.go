package observability

import "testing"

func TestAzureCountFromResponse(t *testing.T) {
	cases := []struct {
		name    string
		resp    AzureResponse
		want    int
		wantErr bool
	}{
		{name: "empty tables -> zero, no panic/err", resp: AzureResponse{}, want: 0},
		{name: "empty rows -> zero", resp: AzureResponse{Tables: []Table{{Rows: [][]any{}}}}, want: 0},
		{name: "empty first row -> zero", resp: AzureResponse{Tables: []Table{{Rows: [][]any{{}}}}}, want: 0},
		{name: "float64 count", resp: AzureResponse{Tables: []Table{{Rows: [][]any{{float64(42)}}}}}, want: 42},
		{name: "int count", resp: AzureResponse{Tables: []Table{{Rows: [][]any{{7}}}}}, want: 7},
		{name: "unknown type -> err", resp: AzureResponse{Tables: []Table{{Rows: [][]any{{"oops"}}}}}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := azureCountFromResponse(tc.resp)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestAzureLabelValuesFromResponse(t *testing.T) {
	// Empty / malformed responses must yield an empty slice, never panic.
	if got := azureLabelValuesFromResponse(AzureResponse{}); len(got) != 0 {
		t.Errorf("empty tables: got %v, want []", got)
	}
	resp := AzureResponse{Tables: []Table{{Rows: [][]any{
		{"GET /a"},
		{},    // empty row must be skipped, not panic
		{nil}, // nil first value must be skipped, not stringified to "<nil>"
		{"POST /b", "ignored-second-col"},
	}}}}
	got := azureLabelValuesFromResponse(resp)
	want := []string{"GET /a", "POST /b"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

// node-agent emits the destination zone under a misspelled key ("availablity"); the converter
// has to accept it, and the correct spelling for an agent that later fixes it (#40498).
func TestConvertAzureTraceReadsDestinationZoneUnderBothSpellings(t *testing.T) {
	for _, key := range []string{"destination.cloud.availablity_zone", "destination.cloud.availability_zone"} {
		t.Run(key, func(t *testing.T) {
			row := map[string]any{"customDimensions": `{"` + key + `":"eastus-1"}`}
			trace := (&AzureAppInsightsTraceSource{}).convertAzureTraceToOpenteleMetry(row, []Column{{Name: "customDimensions"}})
			if got := trace.ResourceAttributes["cloud.availability_zone"]; got != "eastus-1" {
				t.Fatalf("cloud.availability_zone = %q, want %q", got, "eastus-1")
			}
		})
	}
}
