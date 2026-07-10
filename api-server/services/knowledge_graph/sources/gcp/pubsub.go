package gcp

import (
	"encoding/json"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
	"strings"
)

// pubSubTopicSchema is the concrete per-specific_type property schema for the
// PubSubTopic specific_type. Pub/Sub topics are ingested via the shared
// classify.go path and carry only universal base keys (name/region/subtype) plus
// the raw `meta`; subscription linkage (createPubSubSubscriptionEdges) reads
// resource-row meta, not hoisted node properties, so no resource-specific field
// is declared.
var pubSubTopicSchema = core.SpecificTypeSchema{
	SpecificType: "PubSubTopic",
	NodeType:     core.NodeTypeTopic,
}

func init() { core.RegisterSpecificTypeSchema(pubSubTopicSchema) }

// createCronJobEdges links each Cloud Scheduler CronJob to what it triggers: the
// Pub/Sub Topic it publishes to (PUBLISHES_TO), or the Cloud Run / App Engine service
// its HTTP/App Engine target hits (CALLS) — resolved through the same host→service map
// used for Pub/Sub push endpoints (run.app, LB-fronted custom domains, appspot).
func (s *GCPSource) createCronJobEdges(cliData *gcpCLIData, lookup *sources.NodeLookup, backendTargets map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)
	cronNodes, ok := lookup.ByNodeType[core.NodeTypeCronJob]
	if !ok {
		return edges
	}
	hostConsumer, appEngineNode := s.buildPushConsumerHostMap(cliData, lookup, backendTargets)

	for _, node := range cronNodes {
		if e := s.cronJobTargetEdge(node, hostConsumer, appEngineNode, lookup, req); e != nil {
			edges = append(edges, e)
		}
	}

	s.logger.Info("created GCP Cloud Scheduler → target edges", "edge_count", len(edges))
	return edges
}

// cronJobTargetEdge resolves a single CronJob's target to a graph node, or nil.
func (s *GCPSource) cronJobTargetEdge(node *core.DbNode, hostConsumer map[string]*core.DbNode, appEngineNode *core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) *core.DbEdge {
	meta, _ := node.Properties["meta"].(map[string]interface{})
	if meta == nil {
		return nil
	}
	targetType, _ := meta["target_type"].(string)
	target, _ := meta["target"].(string)
	if target == "" {
		return nil
	}

	switch targetType {
	case "pubsub":
		// target is "projects/{p}/topics/{name}"; link cron PUBLISHES_TO topic.
		if t := findNodeByNameAndType(lookup, core.NodeTypeTopic, extractGCPShortName(target)); t != nil {
			return s.createEdge(node, t, core.RelationshipPublishesTo,
				map[string]interface{}{"connection_type": "scheduler_target"}, req)
		}
	case "appengine":
		// App Engine target carries only a relative URI; it hits the account's App Engine app.
		if appEngineNode != nil {
			return s.createEdge(node, appEngineNode, core.RelationshipCalls,
				map[string]interface{}{"connection_type": "scheduler_target"}, req)
		}
	case "http":
		host := strings.ToLower(sources.RepoURIHost(target))
		consumer := hostConsumer[host]
		if consumer == nil && appEngineNode != nil && strings.HasSuffix(host, ".appspot.com") {
			consumer = appEngineNode
		}
		if consumer != nil {
			return s.createEdge(node, consumer, core.RelationshipCalls,
				map[string]interface{}{"connection_type": "scheduler_target"}, req)
		}
	}
	return nil
}

// createPubSubSubscriptionEdges links the consumer of a push Pub/Sub subscription
// to the topic it subscribes to (consumer SUBSCRIBES_TO topic). Subscriptions are
// not modelled as standalone nodes — the GCP service-type filter only admits
// topics — but their collected metadata (the topic and the push endpoint) lets us
// connect a Cloud Run service that receives events to the Topic feeding it. This is
// the only in-graph signal that wires the serverless app tier to Pub/Sub.
//
// Pull subscriptions (no push endpoint) and subscriptions pushing to external,
// non-Cloud-Run endpoints (webhooks) have no in-graph consumer node and are skipped
// rather than guessed at.
func (s *GCPSource) createPubSubSubscriptionEdges(resources []sources.CloudResourceRow, cliData *gcpCLIData, lookup *sources.NodeLookup, backendTargets map[string]*core.DbNode, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	hostConsumer, appEngineNode := s.buildPushConsumerHostMap(cliData, lookup, backendTargets)
	if len(hostConsumer) == 0 && appEngineNode == nil {
		return edges
	}

	for i := range resources {
		edges = append(edges, s.pushSubscriptionEdges(&resources[i], hostConsumer, appEngineNode, lookup, req)...)
	}

	s.logger.Info("created GCP Pub/Sub subscription edges", "edge_count", len(edges))
	return edges
}

// buildPushConsumerHostMap maps every hostname a Pub/Sub push endpoint might target
// to its in-graph consumer node, and returns the account's App Engine node for the
// appspot fallback. Three sources of hosts:
//   - Cloud Run run.app hosts (the ServerlessFunction dns_name);
//   - custom domains served by the external HTTPS LB — resolved host → url-map
//     hostRule → path-matcher default backend → backend's Cloud Run/App Engine target;
//   - (appspot hosts are handled by the caller via the returned App Engine node).
func (s *GCPSource) buildPushConsumerHostMap(cliData *gcpCLIData, lookup *sources.NodeLookup, backendTargets map[string]*core.DbNode) (map[string]*core.DbNode, *core.DbNode) {
	hostConsumer := make(map[string]*core.DbNode)

	_, appEngineNode := indexServerlessNodes(lookup)
	if srvNodes, ok := lookup.ByNodeType[core.NodeTypeServerlessFunction]; ok {
		for _, n := range srvNodes {
			if dns, _ := n.Properties["dns_name"].(string); dns != "" {
				hostConsumer[strings.ToLower(dns)] = n
			}
		}
	}

	if cliData != nil {
		for _, um := range cliData.urlMaps {
			addURLMapHostTargets(um, backendTargets, hostConsumer)
		}
	}

	return hostConsumer, appEngineNode
}

// pushSubscriptionEdges returns the consumer→topic edges contributed by a single
// resource row, or nil when the row isn't a push subscription, lacks a topic, or
// pushes to an endpoint that isn't a known Cloud Run service.
func (s *GCPSource) pushSubscriptionEdges(r *sources.CloudResourceRow, hostConsumer map[string]*core.DbNode, appEngineNode *core.DbNode, lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	if !strings.EqualFold(r.Type, "pubsub.googleapis.com/Subscription") || len(r.Meta) == 0 {
		return nil
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(r.Meta, &meta); err != nil {
		return nil
	}
	topicName, _ := meta["topic"].(string)
	pushEndpoint, _ := meta["push_endpoint"].(string)
	if topicName == "" || pushEndpoint == "" {
		return nil
	}
	host := strings.ToLower(sources.RepoURIHost(pushEndpoint))
	if host == "" {
		return nil
	}
	consumer := hostConsumer[host]
	// appspot.com hosts are App Engine's default/custom domains; map to the account's
	// App Engine node when not already resolved via a run.app or LB-routed custom host.
	if consumer == nil && appEngineNode != nil && strings.HasSuffix(host, ".appspot.com") {
		consumer = appEngineNode
	}
	if consumer == nil {
		return nil // external webhook / unknown consumer — no in-graph node
	}

	// meta.topic is normally the short topic id (collector shortens it), but guard
	// against a full resource path so the name lookup doesn't miss.
	topicNodes := lookup.GetNodesByTypeAndName(core.NodeTypeTopic, extractGCPShortName(topicName))
	edges := make([]*core.DbEdge, 0, len(topicNodes))
	for _, topicNode := range topicNodes {
		edges = append(edges, s.createEdge(consumer, topicNode, core.RelationshipSubscribesTo,
			map[string]interface{}{
				"connection_type": "pubsub_push_subscription",
				"subscription":    r.Name,
			}, req))
	}
	return edges
}
