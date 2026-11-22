package vmservicemap

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"nudgebee/services/traces"
)

// Resolution says how a node was identified, kept on it as a label so a wrong
// match can be traced to the rule that made it.
const (
	resolvedByAgent = "agent"
)

type node struct {
	id       traces.ServiceApplicationId
	category string
	labels   map[string]string
}

type linkKey struct{ src, dst string }

type linkAgg struct {
	requests, errors, latencyWeighted, bytesSent, bytesRecv float64
	// requestsByProtocol sums requests per protocol across every edge folded
	// into this link, so the protocol shown is the one that carried the most
	// traffic overall — not just the busiest single edge (e.g. two HTTP edges
	// at 100 requests each must outrank one TCP edge at 150).
	requestsByProtocol map[string]float64
}

// dominantProtocol is the protocol with the highest summed request count;
// ties favor the alphabetically earlier name for a stable result.
func (a *linkAgg) dominantProtocol() string {
	best, bestRequests := "", -1.0
	for protocol, requests := range a.requestsByProtocol {
		if requests > bestRequests || (requests == bestRequests && protocol < best) {
			best, bestRequests = protocol, requests
		}
	}
	return best
}

// hostNames gives each reporting host the name its services sit under: the
// hostname, with the instance id added when several hosts share one so their
// services stay apart.
type hostNames map[string]string

func newHostNames(hosts []Host, services []Service, edges []Edge) hostNames {
	type h struct{ hostname, instanceID string }
	seen := map[string]h{}
	add := func(hostname, instanceID string) {
		if hostname == "" && instanceID == "" {
			return
		}
		seen[hostname+"\x00"+instanceID] = h{hostname, instanceID}
	}
	for _, x := range hosts {
		add(x.Hostname, x.InstanceID)
	}
	for _, x := range services {
		add(x.Hostname, x.InstanceID)
	}
	for _, e := range edges {
		add(e.SourceHost, e.SourceInstanceID)
		add(e.DestinationHost, e.DestinationInstanceID)
	}
	byHostname := map[string]int{}
	for _, x := range seen {
		byHostname[x.hostname]++
	}
	out := hostNames{}
	for k, x := range seen {
		name := x.hostname
		if byHostname[x.hostname] > 1 && x.instanceID != "" {
			name = fmt.Sprintf("%s (%s)", x.hostname, x.instanceID)
		}
		out[k] = name
	}
	return out
}

func (n hostNames) of(hostname, instanceID string) string {
	if name, ok := n[hostname+"\x00"+instanceID]; ok && name != "" {
		return name
	}
	if hostname != "" {
		return hostname
	}
	return instanceID
}

func hostPart(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

func portPart(addr string) string {
	if _, p, err := net.SplitHostPort(addr); err == nil {
		return p
	}
	return ""
}

func serviceNodeKind(kind string) string {
	switch kind {
	case "systemd":
		return NodeKindSystemd
	case "docker":
		return NodeKindDocker
	default:
		return NodeKindProcess
	}
}

// Build turns a forager answer into a service map: one node per active service
// on a reporting host, and a link per call between services. The forager has
// already resolved every destination that is a reporting host.
func Build(resp Response, now time.Time) traces.ServiceMap {
	hosts := newHostNames(resp.Hosts, resp.Services, resp.Edges)

	nodes := map[string]*node{}
	order := []string{}
	links := map[linkKey]*linkAgg{}

	addNode := func(id traces.ServiceApplicationId, category string, labels map[string]string) string {
		id.Name = nodeName(id.Name)
		key := id.Namespace + ":" + id.Kind + ":" + id.Name
		if _, ok := nodes[key]; !ok {
			nodes[key] = &node{id: id, category: category, labels: labels}
			order = append(order, key)
		}
		return key
	}
	serviceNode := func(host, name, kind string, labels map[string]string) string {
		labels["host"] = host
		labels["source"] = resolvedByAgent
		return addNode(traces.ServiceApplicationId{Name: name, Kind: serviceNodeKind(kind), Namespace: nodeName(host)}, "application", labels)
	}

	// Every active service is a node, called or not.
	for _, sv := range resp.Services {
		labels := map[string]string{"instance_id": sv.InstanceID, "service_kind": sv.Kind, "container_id": sv.ContainerID}
		if sv.Image != "" {
			labels["image"] = sv.Image
		}
		if len(sv.ApplicationTypes) > 0 {
			labels["application_types"] = strings.Join(sv.ApplicationTypes, ",")
		}
		if len(sv.Listeners) > 0 {
			ports := make([]string, 0, len(sv.Listeners))
			for _, l := range sv.Listeners {
				ports = append(ports, fmt.Sprint(l.Port))
			}
			labels["listen_ports"] = strings.Join(uniqueStrings(ports), ",")
		}
		serviceNode(hosts.of(sv.Hostname, sv.InstanceID), sv.Name, sv.Kind, labels)
	}

	for _, e := range resp.Edges {
		if e.SourceService == "" {
			continue
		}
		srcHost := hosts.of(e.SourceHost, e.SourceInstanceID)
		src := serviceNode(srcHost, e.SourceService, kindFromContainerID(e.SourceContainerID),
			map[string]string{"instance_id": e.SourceInstanceID, "container_id": e.SourceContainerID})

		dst := resolveDestination(e, hosts, addNode, serviceNode)

		k := linkKey{src, dst}
		agg := links[k]
		if agg == nil {
			agg = &linkAgg{}
			links[k] = agg
		}
		agg.requests += e.Requests
		agg.errors += e.Errors
		agg.latencyWeighted += e.LatencyMs * e.Requests
		agg.bytesSent += e.BytesSent
		agg.bytesRecv += e.BytesReceived
		if agg.requestsByProtocol == nil {
			agg.requestsByProtocol = map[string]float64{}
		}
		agg.requestsByProtocol[e.Protocol] += e.Requests
	}

	apps := make([]traces.ServiceApplication, 0, len(order))
	for _, key := range order {
		n := nodes[key]
		apps = append(apps, traces.ServiceApplication{
			Id:               n.id,
			Category:         traces.ServiceCategory{Category: n.category},
			Labels:           n.labels,
			Indicators:       []string{},
			Upstreams:        []traces.UpstreamLink{},
			Downstreams:      []traces.DownstreamLink{},
			Instances:        []traces.Instance{{Id: n.id}},
			Type:             []string{},
			DesiredInstances: 1,
			IsHealthy:        true,
		})
	}
	byKey := map[string]int{}
	for i, key := range order {
		byKey[key] = i
	}

	keys := make([]linkKey, 0, len(links))
	for k := range links {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].src != keys[j].src {
			return keys[i].src < keys[j].src
		}
		return keys[i].dst < keys[j].dst
	})
	for _, k := range keys {
		agg := links[k]
		latency := 0.0
		if agg.requests > 0 {
			latency = agg.latencyWeighted / agg.requests
		}
		status := 0
		if agg.errors > 0 {
			status = 1
		}
		stats := []string{fmt.Sprintf("%.0f requests", agg.requests), fmt.Sprintf("%.0f errors", agg.errors)}
		protocol := agg.dominantProtocol()
		s, d := apps[byKey[k.src]], apps[byKey[k.dst]]
		apps[byKey[k.src]].Upstreams = append(apps[byKey[k.src]].Upstreams, traces.UpstreamLink{
			Id: linkID(d.Id), Status: status, Stats: stats, Latency: latency,
			RequestCount: agg.requests, FailureCount: agg.errors, Protocol: protocol,
			BytesSent: agg.bytesSent, BytesReceived: agg.bytesRecv,
		})
		apps[byKey[k.dst]].Downstreams = append(apps[byKey[k.dst]].Downstreams, traces.DownstreamLink{
			Id: s.Id, Status: status, Stats: stats, Latency: latency,
			RequestCount: agg.requests, FailureCount: agg.errors, Protocol: protocol,
			BytesSent: agg.bytesSent, BytesReceived: agg.bytesRecv,
		})
	}

	return traces.ServiceMap{Applications: apps, GeneratedAt: now, Labels: []string{}}
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func kindFromContainerID(containerID string) string {
	switch {
	case strings.HasPrefix(containerID, "/docker/"):
		return "docker"
	case strings.HasSuffix(containerID, ".service"):
		return "systemd"
	default:
		return "other"
	}
}

// namespaceFor groups the nodes that are not a service on a host by what they
// are. The map UI shows the namespace as the node's subtitle and builds link
// targets from "namespace:kind:name", so it has to be non-empty and colon-free.
func namespaceFor(kind string) string {
	switch kind {
	case NodeKindUnknownInternal:
		return "unknown-internal"
	default:
		return "external"
	}
}

// nodeName keeps a name usable inside "namespace:kind:name" — the map UI splits
// link ids on ":" — which an IPv6 address or a host:port fallback would break.
func nodeName(name string) string {
	return strings.ReplaceAll(name, ":", "_")
}

func linkID(id traces.ServiceApplicationId) string {
	return fmt.Sprintf("%s:%s:%s", id.Namespace, id.Kind, id.Name)
}

// resolveDestination names the node a call lands on, in this order:
//  1. the service the forager found listening there (a reporting host);
//  2. a reporting host with nothing listening on that port: an unknown process on it;
//  3. otherwise an unknown internal host (private, by address) or an external
//     service (by DNS name, or address when none was resolved).
func resolveDestination(
	e Edge, hosts hostNames,
	addNode func(traces.ServiceApplicationId, string, map[string]string) string,
	serviceNode func(host, name, kind string, labels map[string]string) string,
) string {
	if e.DestinationHost != "" || e.DestinationInstanceID != "" {
		host := hosts.of(e.DestinationHost, e.DestinationInstanceID)
		if e.DestinationService != "" {
			return serviceNode(host, e.DestinationService, kindFromContainerID(e.DestinationContainerID),
				map[string]string{"instance_id": e.DestinationInstanceID, "container_id": e.DestinationContainerID})
		}
		name := "port " + portPart(e.Destination)
		if portPart(e.Destination) == "" {
			name = "unknown process"
		}
		return addNode(traces.ServiceApplicationId{Name: name, Kind: NodeKindUnknownProcess, Namespace: nodeName(host)}, "application",
			map[string]string{"host": host, "address": e.Destination, "instance_id": e.DestinationInstanceID, "source": resolvedByAgent})
	}

	if e.DestinationKind == KindPrivate || e.DestinationKind == KindVM {
		ip := hostPart(e.Destination)
		return addNode(traces.ServiceApplicationId{Name: ip, Kind: NodeKindUnknownInternal, Namespace: namespaceFor(NodeKindUnknownInternal)}, "external",
			map[string]string{"address": ip})
	}
	name := e.DestinationName
	if name == "" {
		name = hostPart(strings.TrimSpace(e.Destination))
	}
	return addNode(traces.ServiceApplicationId{Name: name, Kind: NodeKindExternal, Namespace: namespaceFor(NodeKindExternal)}, "external",
		map[string]string{"address": e.Destination})
}
