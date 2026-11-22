package sources

import (
	"fmt"
	"testing"
)

func entries(spec map[string][]string) []*EnricherRegistryEntry {
	out := make([]*EnricherRegistryEntry, 0, len(spec))
	for name, after := range spec {
		out = append(out, &EnricherRegistryEntry{Name: name, RunsAfter: after})
	}
	return out
}

func TestOrderEnrichers(t *testing.T) {
	cases := []struct {
		name string
		spec map[string][]string
		want []string
	}{
		{
			name: "no dependencies is alphabetical",
			spec: map[string][]string{"c": nil, "a": nil, "b": nil},
			want: []string{"a", "b", "c"},
		},
		{
			name: "a dependency outranks the alphabet",
			spec: map[string][]string{"aws_lb_k8s": {"k8s_ec2"}, "k8s_ec2": nil, "identity": nil},
			want: []string{"identity", "k8s_ec2", "aws_lb_k8s"},
		},
		{
			name: "chains are followed",
			spec: map[string][]string{"a": {"b"}, "b": {"c"}, "c": nil},
			want: []string{"c", "b", "a"},
		},
		{
			name: "an unregistered dependency is ignored",
			spec: map[string][]string{"x": {"not-registered"}, "w": nil},
			want: []string{"w", "x"},
		},
		{
			name: "a self-dependency is ignored",
			spec: map[string][]string{"x": {"x"}},
			want: []string{"x"},
		},
		{
			name: "a cycle runs last, in name order, and nothing is dropped",
			spec: map[string][]string{"p": {"q"}, "q": {"p"}, "r": nil},
			want: []string{"r", "p", "q"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The input order comes from ranging over a map; build it many times
			// so a dependence on that order would show up as a flaky mismatch.
			for i := 0; i < 50; i++ {
				got := orderEnrichers(entries(tc.spec))
				if fmt.Sprint(got) != fmt.Sprint(tc.want) {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// The registry the queue consumer actually uses must come back in one order,
// every time, with every declared dependency satisfied.
func TestRegisteredEnricherOrderIsStable(t *testing.T) {
	first := ListRegisteredEnrichers()
	if len(first) == 0 {
		t.Fatal("no enrichers registered")
	}
	for i := 0; i < 100; i++ {
		if got := ListRegisteredEnrichers(); fmt.Sprint(got) != fmt.Sprint(first) {
			t.Fatalf("order changed between calls:\n first %v\n later %v", first, got)
		}
	}

	pos := make(map[string]int, len(first))
	for i, name := range first {
		pos[name] = i
	}
	if pos["k8s_ec2"] > pos["aws_lb_k8s"] {
		t.Errorf("aws_lb_k8s runs before k8s_ec2, whose Node → EC2 edges it walks: %v", first)
	}
}

// Every RunsAfter must name a registered enricher, and the declarations must not
// form a cycle. orderEnrichers tolerates both at runtime so a bad registration
// can't stop builds — this is where they are caught instead.
func TestRegisteredEnricherDependencies(t *testing.T) {
	registryMutex.RLock()
	all := make([]*EnricherRegistryEntry, 0, len(enricherRegistry))
	for _, e := range enricherRegistry {
		all = append(all, e)
	}
	registryMutex.RUnlock()

	registered := make(map[string]bool, len(all))
	for _, e := range all {
		registered[e.Name] = true
	}
	for _, e := range all {
		for _, dep := range e.RunsAfter {
			if !registered[dep] {
				t.Errorf("%s runs after %q, which is not a registered enricher", e.Name, dep)
			}
		}
	}

	order := orderEnrichers(all)
	pos := make(map[string]int, len(order))
	for i, name := range order {
		pos[name] = i
	}
	for _, e := range all {
		for _, dep := range e.RunsAfter {
			if registered[dep] && pos[dep] > pos[e.Name] {
				t.Errorf("%s is ordered before its dependency %s — the declarations form a cycle", e.Name, dep)
			}
		}
	}
}
