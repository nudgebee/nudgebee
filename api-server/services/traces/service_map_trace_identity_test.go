package traces

import "testing"

func TestBuildServiceMap_ParentLinksStayWithinTrace(t *testing.T) {
	builder := NewTraceServiceMapBuilder()
	builder.AddSpans([]TraceSpan{
		span("trace-a", "shared-parent", "", "checkout", "prod"),
		span("trace-a", "child-a", "shared-parent", "billing", "prod"),
		span("trace-b", "shared-parent", "", "portal", "prod"),
		span("trace-b", "child-b", "shared-parent", "search", "prod"),
	})

	sm, err := builder.BuildServiceMap()
	if err != nil {
		t.Fatal(err)
	}
	for caller, want := range map[string]string{
		"checkout": "prod:Service:billing",
		"portal":   "prod:Service:search",
	} {
		app := appFor(sm, "prod", caller)
		if app == nil {
			t.Fatalf("missing application %q", caller)
		}
		links := upstreamIDs(app)
		if len(links) != 1 || links[0] != want {
			t.Errorf("%s upstreams = %v, want [%s]", caller, links, want)
		}
	}
}

func TestBuildServiceMap_MissingTraceIDDoesNotCreateParentLink(t *testing.T) {
	builder := NewTraceServiceMapBuilder()
	builder.AddSpans([]TraceSpan{
		span("", "parent", "", "checkout", "prod"),
		span("", "child", "parent", "billing", "prod"),
	})
	sm, err := builder.BuildServiceMap()
	if err != nil {
		t.Fatal(err)
	}
	app := appFor(sm, "prod", "checkout")
	if app == nil {
		t.Fatal("missing checkout application")
	}
	if links := upstreamIDs(app); len(links) != 0 {
		t.Errorf("upstreams with unknown trace identity = %v, want none", links)
	}
}

func TestBuildServiceMap_MissingSpanIDDoesNotConnectRoots(t *testing.T) {
	builder := NewTraceServiceMapBuilder()
	builder.AddSpans([]TraceSpan{
		span("trace-a", "", "", "checkout", "prod"),
		span("trace-a", "root", "", "billing", "prod"),
	})
	sm, err := builder.BuildServiceMap()
	if err != nil {
		t.Fatal(err)
	}
	app := appFor(sm, "prod", "checkout")
	if app == nil {
		t.Fatal("missing checkout application")
	}
	if links := upstreamIDs(app); len(links) != 0 {
		t.Errorf("upstreams with unknown span identity = %v, want none", links)
	}
}
