package account

import (
	"reflect"
	"testing"
)

func TestNormalizeAWSRegions(t *testing.T) {
	cases := []struct {
		name    string
		in      []string
		want    []string
		wantErr bool
	}{
		{"nil is no list", nil, nil, false},
		{"empty is no list", []string{}, nil, false},
		{"single region", []string{"ap-southeast-2"}, []string{"ap-southeast-2"}, false},
		{"order preserved", []string{"us-east-1", "ap-southeast-2"}, []string{"us-east-1", "ap-southeast-2"}, false},
		{"duplicates collapse", []string{"ap-southeast-2", "ap-southeast-2"}, []string{"ap-southeast-2"}, false},
		{"whitespace trimmed", []string{"  ap-southeast-2  "}, []string{"ap-southeast-2"}, false},
		{"blank entries skipped", []string{"ap-southeast-2", "   "}, []string{"ap-southeast-2"}, false},
		{"gov region accepted", []string{"us-gov-east-1"}, []string{"us-gov-east-1"}, false},
		// A typo must fail the request rather than produce an allowlist that
		// crawls nothing while the account reports healthy.
		{"typo rejected", []string{"ap-southeast2"}, nil, true},
		{"uppercase rejected", []string{"AP-SOUTHEAST-2"}, nil, true},
		{"non-region word rejected", []string{"global"}, nil, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := normalizeAWSRegions(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("normalizeAWSRegions(%v) = %v, want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeAWSRegions(%v) returned unexpected error: %v", c.in, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("normalizeAWSRegions(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestApplyAWSRegions(t *testing.T) {
	t.Run("no regions leaves data and region untouched", func(t *testing.T) {
		data := map[string]any{"cost_report_name": "nudgebeeReport"}
		got, region, err := applyAWSRegions(data, nil, "us-east-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if region != "us-east-1" {
			t.Fatalf("region = %q, want us-east-1", region)
		}
		if _, ok := got[AccountRegionsKey]; ok {
			t.Fatal("did not expect a regions key")
		}
	})

	t.Run("allowlist is stored and preserves other data keys", func(t *testing.T) {
		data := map[string]any{"cost_report_name": "nudgebeeReport"}
		got, region, err := applyAWSRegions(data, []string{"ap-southeast-2"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if region != "ap-southeast-2" {
			t.Fatalf("region = %q, want ap-southeast-2", region)
		}
		if got["cost_report_name"] != "nudgebeeReport" {
			t.Fatal("billing config was dropped")
		}
		if !reflect.DeepEqual(got[AccountRegionsKey], []string{"ap-southeast-2"}) {
			t.Fatalf("regions = %v", got[AccountRegionsKey])
		}
	})

	t.Run("nil data map is created", func(t *testing.T) {
		got, _, err := applyAWSRegions(nil, []string{"ap-southeast-2"}, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got[AccountRegionsKey], []string{"ap-southeast-2"}) {
			t.Fatalf("regions = %v", got[AccountRegionsKey])
		}
	})

	t.Run("bootstrap region outside the allowlist is corrected", func(t *testing.T) {
		// A role scoped with aws:RequestedRegion denies every call made outside
		// the allowlist, so bootstrapping in us-east-1 would fail immediately.
		_, region, err := applyAWSRegions(nil, []string{"ap-southeast-2"}, "us-east-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if region != "ap-southeast-2" {
			t.Fatalf("region = %q, want ap-southeast-2", region)
		}
	})

	t.Run("bootstrap region inside the allowlist is kept", func(t *testing.T) {
		_, region, err := applyAWSRegions(nil, []string{"ap-southeast-2", "us-east-1"}, "us-east-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if region != "us-east-1" {
			t.Fatalf("region = %q, want us-east-1", region)
		}
	})

	t.Run("invalid region fails the request", func(t *testing.T) {
		if _, _, err := applyAWSRegions(nil, []string{"ap-southeast2"}, ""); err == nil {
			t.Fatal("expected an error for a malformed region")
		}
	})
}

func TestApplyRegionsToData(t *testing.T) {
	t.Run("allowlist is added without disturbing existing keys", func(t *testing.T) {
		existing := map[string]any{"cost_report_name": "nudgebeeReport"}
		got, region := applyRegionsToData(existing, []string{"ap-southeast-2"}, nil)
		if got["cost_report_name"] != "nudgebeeReport" {
			t.Fatal("billing config was dropped")
		}
		if region != "ap-southeast-2" {
			t.Fatalf("region = %q", region)
		}
	})

	t.Run("empty allowlist clears the key and keeps the rest", func(t *testing.T) {
		existing := map[string]any{
			"cost_report_name": "nudgebeeReport",
			AccountRegionsKey:  []any{"ap-southeast-2"},
		}
		got, region := applyRegionsToData(existing, nil, nil)
		if _, ok := got[AccountRegionsKey]; ok {
			t.Fatal("expected the allowlist to be cleared")
		}
		if got["cost_report_name"] != "nudgebeeReport" {
			t.Fatal("billing config was dropped while clearing regions")
		}
		// Clearing returns the account to auto-discovery; the stored bootstrap
		// region stays valid, so there is nothing to write.
		if region != "" {
			t.Fatalf("region = %q, want empty", region)
		}
	})

	t.Run("same-request data edits are applied and preserved", func(t *testing.T) {
		existing := map[string]any{"cost_report_name": "old"}
		got, _ := applyRegionsToData(existing, []string{"ap-southeast-2"}, map[string]any{"cost_report_name": "new"})
		if got["cost_report_name"] != "new" {
			t.Fatalf("cost_report_name = %v, want new", got["cost_report_name"])
		}
	})

	t.Run("a raw regions value in data cannot clobber the validated allowlist", func(t *testing.T) {
		// data and regions can arrive in the same request. The validated list
		// must win, or an unvalidated string reaches the collector.
		got, _ := applyRegionsToData(nil, []string{"ap-southeast-2"}, map[string]any{AccountRegionsKey: "nonsense"})
		if !reflect.DeepEqual(got[AccountRegionsKey], []string{"ap-southeast-2"}) {
			t.Fatalf("regions = %v, want the validated list", got[AccountRegionsKey])
		}
	})

	t.Run("nil existing map is created", func(t *testing.T) {
		got, _ := applyRegionsToData(nil, []string{"ap-southeast-2"}, nil)
		if !reflect.DeepEqual(got[AccountRegionsKey], []string{"ap-southeast-2"}) {
			t.Fatalf("regions = %v", got[AccountRegionsKey])
		}
	})
}

func TestCarryRegionsInto(t *testing.T) {
	t.Run("stored allowlist survives an unrelated data write", func(t *testing.T) {
		// Editing the CUR config must not silently return the account to region
		// auto-discovery — that breaks collection for a region-scoped role.
		existing := map[string]any{AccountRegionsKey: []any{"ap-southeast-2"}}
		incoming := map[string]any{"cost_report_name": "nudgebeeReport"}

		got := carryRegionsInto(incoming, existing)

		if !reflect.DeepEqual(got[AccountRegionsKey], []any{"ap-southeast-2"}) {
			t.Fatalf("regions = %v, want the stored allowlist", got[AccountRegionsKey])
		}
		if got["cost_report_name"] != "nudgebeeReport" {
			t.Fatal("the caller's own edit was lost")
		}
		if _, ok := incoming[AccountRegionsKey]; ok {
			t.Fatal("the caller's map was mutated")
		}
	})

	t.Run("no stored allowlist changes nothing", func(t *testing.T) {
		incoming := map[string]any{"cost_report_name": "nudgebeeReport"}
		got := carryRegionsInto(incoming, map[string]any{"cost_report_name": "old"})
		if _, ok := got[AccountRegionsKey]; ok {
			t.Fatal("invented an allowlist")
		}
	})

	t.Run("an explicit regions key in the payload wins", func(t *testing.T) {
		existing := map[string]any{AccountRegionsKey: []any{"ap-southeast-2"}}
		incoming := map[string]any{AccountRegionsKey: []any{"us-east-1"}}
		got := carryRegionsInto(incoming, existing)
		if !reflect.DeepEqual(got[AccountRegionsKey], []any{"us-east-1"}) {
			t.Fatalf("regions = %v, want the caller's value", got[AccountRegionsKey])
		}
	})
}
