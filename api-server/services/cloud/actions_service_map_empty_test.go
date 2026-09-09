package cloud

import "testing"

// serviceMapHasDependency mirrors the guard in cloudServiceMapAction: a map is
// worth emitting only when something in it has a dependency. Kept as a helper
// so the rule can be tested without standing up a provider, a request context
// and a live cloud account.
func serviceMapHasDependency(apps []ServiceMapApplication) bool {
	for _, app := range apps {
		if len(app.Upstreams) > 0 || len(app.Downstreams) > 0 {
			return true
		}
	}
	return false
}

// The cloud service map returns the seed resource even when it discovers
// nothing about it - one application, no upstreams, no downstreams, Status
// "Unknown". Every EC2 alarm we measured produced exactly that.
//
// It is not harmless. The cloud card is written before the knowledge-graph
// card, and correlation used to take the first service_map evidence it found,
// so cloud events built their dependency graph from the empty one and could
// never score a hop. Correlation now prefers the knowledge-graph card, but the
// empty card still renders, telling an operator a database alarm has no callers
// while the same event carries the "instance CALLS database" edge from flow
// logs.
func TestServiceMapHasDependency_ContainmentOnlyIsNotEmitted(t *testing.T) {
	apps := []ServiceMapApplication{{
		Id:          ServiceApplicationId{Name: "i-0b079820a95b1517a", Kind: "AmazonEC2"},
		Upstreams:   []UpstreamLink{},
		Downstreams: []DownstreamLink{},
		Status:      "Unknown",
	}}
	if serviceMapHasDependency(apps) {
		t.Error("a map with no upstreams or downstreams must not be emitted; " +
			"it duplicates the knowledge-graph card without its edges")
	}
}

// One real link anywhere in the map is enough to be worth showing.
func TestServiceMapHasDependency_EmitsWhenALinkExists(t *testing.T) {
	apps := []ServiceMapApplication{
		{
			Id:     ServiceApplicationId{Name: "orders-web", Kind: "AmazonEC2"},
			Status: "Active",
		},
		{
			Id:          ServiceApplicationId{Name: "orders-api", Kind: "AmazonEC2"},
			Downstreams: []DownstreamLink{{}},
			Status:      "Active",
		},
	}
	if !serviceMapHasDependency(apps) {
		t.Error("a map containing a downstream link must still be emitted")
	}
}

func TestServiceMapHasDependency_EmptySliceIsNotEmitted(t *testing.T) {
	if serviceMapHasDependency(nil) {
		t.Error("an empty application list must not be emitted")
	}
}
