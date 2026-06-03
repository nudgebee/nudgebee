package kql

import (
	"testing"
)

func TestTranslate(t *testing.T) {
	tests := []struct {
		name    string
		kql     string
		want    string
		wantErr bool
	}{
		{
			name: "simple where clause",
			kql:  `my_table | where status == "200"`,
			want: "{app=\"my_table\"} |~ `status == \"200\"`",
		},
		{
			name: "where with and",
			kql:  `my_table | where status == "200" and method == "GET"`,
			want: "{app=\"my_table\"} |~ `status == \"200\" and method == \"GET\"`",
		},
		{
			name: "where with or",
			kql:  `my_table | where status == "200" or status == "201"`,
			want: "{app=\"my_table\"} |~ `status == \"200\" or status == \"201\"`",
		},
		{
			name: "where with in",
			kql:  `my_table | where status in ("200", "201")`,
			want: "{app=\"my_table\"} |~ `status=~\"200|201\"`",
		},
		{
			name: "project operator",
			kql:  `my_table | project status, method`,
			want: "{app=\"my_table\"} | line_format \"{{status method}}\"",
		},
		{
			name: "take operator",
			kql:  `my_table | take 10`,
			want: "{app=\"my_table\"} | limit 10",
		},
		{
			name: "summarize count",
			kql:  `my_table | summarize count()`,
			want: "{app=\"my_table\"} | count_over_time({app=\"my_table\"}[5m])",
		},
		{
			name: "summarize count by",
			kql:  `my_table | summarize count() by status`,
			want: "{app=\"my_table\"} | sum by (status) (count_over_time({app=\"my_table\"}[5m]))",
		},
		{
			name: "automatic json conversion",
			kql:  `my_table | where request.status == "200"`,
			want: "{app=\"my_table\"} | json |~ `request_status == \"200\"`",
		},
		{
			name: "parse operator",
			kql:  `my_table | parse kind=regex "(?P<user>\\w+)"`,
			want: "{app=\"my_table\"} | regexp \"(?P<user>\\w+)\"",
		},
	}

	k := KqlLokiConverter{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ast, err := Parse(tt.kql)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}

			got, err := k.Translate(*ast)
			if (err != nil) != tt.wantErr {
				t.Errorf("Translate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if got != tt.want {
				t.Errorf("Translate() = %v, want %v", got, tt.want)
			}
		})
	}
}
