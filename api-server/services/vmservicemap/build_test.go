package vmservicemap

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/services/traces"
)

var testNow = time.Date(2026, 10, 13, 10, 15, 0, 0, time.UTC)

func loadFixture(t *testing.T, name string) Response {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	resp, err := parseResult(m)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func findIn(t *testing.T, m traces.ServiceMap, kind, namespace, name string) traces.ServiceApplication {
	t.Helper()
	for _, a := range m.Applications {
		if a.Id.Kind == kind && a.Id.Namespace == namespace && a.Id.Name == name {
			return a
		}
	}
	t.Fatalf("no %s %s/%s in map", kind, namespace, name)
	return traces.ServiceApplication{}
}

func upstream(t *testing.T, a traces.ServiceApplication, kind, namespace, name string) traces.UpstreamLink {
	t.Helper()
	want := namespace + ":" + kind + ":" + name
	for _, u := range a.Upstreams {
		if u.Id == want {
			return u
		}
	}
	t.Fatalf("%s has no upstream %s (has %+v)", a.Id.Name, want, a.Upstreams)
	return traces.UpstreamLink{}
}

func TestBuildPilotServiceToService(t *testing.T) {
	resp := loadFixture(t, "pilot.json")
	m := Build(resp, testNow)

	// Every active service is a node, under the host it runs on.
	if n := len(m.Applications); n != 3+1+1+1 {
		t.Errorf("nodes = %d, want 3 services + an unknown host + 2 external services", n)
	}
	web := findIn(t, m, NodeKindSystemd, "orders-web-host", "orders-web.service")
	api := findIn(t, m, NodeKindSystemd, "orders-api-host", "orders-api.service")
	if web.Labels["listen_ports"] != "80" || api.Labels["application_types"] != "golang" {
		t.Errorf("labels = %v / %v", web.Labels, api.Labels)
	}

	// The two protocols to the same service become one link, summed.
	link := upstream(t, web, NodeKindSystemd, "orders-api-host", "orders-api.service")
	if link.RequestCount != 1005 || link.FailureCount != 10 || link.Status != 1 {
		t.Errorf("web→api = %+v, want 1005 requests, 10 errors, status 1", link)
	}
	if link.Protocol != "http" {
		t.Errorf("protocol = %q, want the one that carried most requests", link.Protocol)
	}
	if got := len(web.Upstreams); got != 1 {
		t.Errorf("web upstreams = %d, want 1", got)
	}

	// A local docker container is a service on the same host.
	upstream(t, api, NodeKindDocker, "orders-api-host", "orders-cache")
	cache := findIn(t, m, NodeKindDocker, "orders-api-host", "orders-cache")
	if cache.Labels["image"] != "redis:7" {
		t.Errorf("cache labels = %v", cache.Labels)
	}

	upstream(t, api, NodeKindExternal, "external", "orders-db.abc.us-east-1.rds.amazonaws.com")
	upstream(t, api, NodeKindUnknownInternal, "unknown-internal", "172.31.9.9")
	upstream(t, api, NodeKindExternal, "external", "api.stripe.com")

	if got := api.Downstreams; len(got) != 1 || got[0].Id.Name != "orders-web.service" || got[0].Id.Namespace != "orders-web-host" {
		t.Errorf("api downstreams = %+v, want only orders-web", got)
	}
}

func TestBuildHostWithNoMatchingListenerIsAnUnknownProcessOnThatHost(t *testing.T) {
	resp := Response{
		Services: []Service{{InstanceID: "i-a", Hostname: "a", ContainerID: "/system.slice/app.service", Name: "app.service", Kind: "systemd"}},
		Edges: []Edge{{
			SourceHost: "a", SourceInstanceID: "i-a", SourceService: "app.service", SourceContainerID: "/system.slice/app.service",
			Destination: "172.31.0.10:9090", DestinationKind: KindPrivate,
			DestinationHost: "prom-host", DestinationInstanceID: "i-prom", Protocol: "http", Requests: 60,
		}},
	}
	m := Build(resp, testNow)
	app := findIn(t, m, NodeKindSystemd, "a", "app.service")
	upstream(t, app, NodeKindUnknownProcess, "prom-host", "port 9090")
}

// TestBuildProtocolIsChosenBySummedRequestsNotTheSingleBusiestEdge guards a
// bug Codex review found: picking the protocol by comparing each incoming
// edge's own request count against the running winner means one busy edge
// of protocol B can outrank several smaller edges of protocol A even though
// A carried more total traffic. Two HTTP edges at 100 requests each (200
// total) must still outrank one TCP edge at 150.
func TestBuildProtocolIsChosenBySummedRequestsNotTheSingleBusiestEdge(t *testing.T) {
	resp := Response{
		Services: []Service{
			{InstanceID: "i-a", Hostname: "a", ContainerID: "/system.slice/app.service", Name: "app.service", Kind: "systemd"},
			{InstanceID: "i-b", Hostname: "b", ContainerID: "/system.slice/api.service", Name: "api.service", Kind: "systemd"},
		},
		Edges: []Edge{
			{SourceHost: "a", SourceInstanceID: "i-a", SourceService: "app.service", SourceContainerID: "/system.slice/app.service",
				DestinationHost: "b", DestinationInstanceID: "i-b", DestinationService: "api.service", DestinationContainerID: "/system.slice/api.service",
				Protocol: "http", Requests: 100},
			{SourceHost: "a", SourceInstanceID: "i-a", SourceService: "app.service", SourceContainerID: "/system.slice/app.service",
				DestinationHost: "b", DestinationInstanceID: "i-b", DestinationService: "api.service", DestinationContainerID: "/system.slice/api.service",
				Protocol: "http", Requests: 100},
			{SourceHost: "a", SourceInstanceID: "i-a", SourceService: "app.service", SourceContainerID: "/system.slice/app.service",
				DestinationHost: "b", DestinationInstanceID: "i-b", DestinationService: "api.service", DestinationContainerID: "/system.slice/api.service",
				Protocol: "tcp", Requests: 150},
		},
	}
	m := Build(resp, testNow)
	app := findIn(t, m, NodeKindSystemd, "a", "app.service")
	link := upstream(t, app, NodeKindSystemd, "b", "api.service")
	if link.Protocol != "http" {
		t.Errorf("protocol = %q, want http (200 summed requests beats tcp's single 150)", link.Protocol)
	}
	if link.RequestCount != 350 {
		t.Errorf("RequestCount = %v, want 350", link.RequestCount)
	}
}

func TestBuildHostnamesSharedAreToldApart(t *testing.T) {
	resp := Response{
		Services: []Service{
			{InstanceID: "i-1", Hostname: "app", ContainerID: "/system.slice/x.service", Name: "x.service", Kind: "systemd"},
			{InstanceID: "i-2", Hostname: "app", ContainerID: "/system.slice/x.service", Name: "x.service", Kind: "systemd"},
		},
	}
	m := Build(resp, testNow)
	findIn(t, m, NodeKindSystemd, "app (i-1)", "x.service")
	findIn(t, m, NodeKindSystemd, "app (i-2)", "x.service")
}

func TestBuildNamesCannotBreakLinkIds(t *testing.T) {
	resp := Response{Edges: []Edge{{
		SourceHost: "h", SourceService: "s.service", SourceContainerID: "/system.slice/s.service",
		Destination: "[fe80::1]:443", DestinationKind: KindExternal, Protocol: "http", Requests: 1,
	}}}
	m := Build(resp, testNow)
	for _, a := range m.Applications {
		for _, u := range a.Upstreams {
			if parts := strings.Split(u.Id, ":"); len(parts) != 3 {
				t.Errorf("link id %q splits into %d parts, the map UI needs 3", u.Id, len(parts))
			}
		}
	}
}

func TestParseResultAcceptsBothEnvelopes(t *testing.T) {
	bare := map[string]any{"hosts": []any{map[string]any{"hostname": "h"}}}
	wrapped := map[string]any{"result": bare, "status_code": 200}
	for name, in := range map[string]map[string]any{"bare": bare, "wrapped": wrapped} {
		resp, err := parseResult(in)
		if err != nil || len(resp.Hosts) != 1 || resp.Hosts[0].Hostname != "h" {
			t.Errorf("%s: resp=%+v err=%v", name, resp, err)
		}
	}
}

func TestFetchSendsWindowAndWrapsError(t *testing.T) {
	var gotAction, gotDS, gotAgent string
	var gotParams map[string]any
	ok := func(_, action, ds string, params map[string]any, _ int, agentType string) (map[string]any, error) {
		gotAction, gotDS, gotAgent, gotParams = action, ds, agentType, params
		return map[string]any{"hosts": []any{}}, nil
	}
	end := testNow
	if _, err := fetch(ok, "acc", Store{DatasourceKey: "ds-key", AgentType: "proxy"}, end.Add(-15*time.Minute), end); err != nil {
		t.Fatal(err)
	}
	if gotAction != "vm_service_map" || gotDS != "ds-key" || gotAgent != "proxy" {
		t.Errorf("action/ds/agent = %q/%q/%q", gotAction, gotDS, gotAgent)
	}
	if gotParams["start"] != "2026-10-13T10:00:00Z" || gotParams["end"] != "2026-10-13T10:15:00Z" {
		t.Errorf("window params = %v", gotParams)
	}

	boom := errors.New("relay down")
	_, err := fetch(func(string, string, string, map[string]any, int, string) (map[string]any, error) { return nil, boom }, "acc", Store{}, end, end)
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want wrapping relay error", err)
	}
}
