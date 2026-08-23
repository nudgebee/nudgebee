package account

import (
	"reflect"
	"testing"
)

func TestArchivableRegions(t *testing.T) {
	cases := []struct {
		name    string
		queried []string
		skipped []string
		want    []string
	}{
		{"nothing skipped", []string{"us-east-1", "eu-west-1"}, nil, []string{"us-east-1", "eu-west-1"}},
		{"one region skipped", []string{"us-east-1", "me-south-1", "eu-west-1"}, []string{"me-south-1"}, []string{"us-east-1", "eu-west-1"}},
		{"all regions skipped yields empty, not nil-scope", []string{"me-south-1", "me-central-1"}, []string{"me-south-1", "me-central-1"}, []string{}},
		{"skipped region not in queried is a no-op", []string{"us-east-1"}, []string{"me-south-1"}, []string{"us-east-1"}},
		{"empty queried stays empty", []string{}, []string{"me-south-1"}, []string{}},
		{"nil queried stays nil", nil, []string{"me-south-1"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := archivableRegions(c.queried, c.skipped)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("archivableRegions(%v, %v) = %v, want %v", c.queried, c.skipped, got, c.want)
			}
		})
	}
}
