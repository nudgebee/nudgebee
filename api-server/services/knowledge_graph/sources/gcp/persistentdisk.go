package gcp

import (
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/knowledge_graph/sources"
)

// persistentDiskSchema is the concrete per-specific_type property schema for
// the PersistentDisk specific_type. Names are the node.Properties keys written by
// extractGCPPersistentDiskMetadata. core.QueryablePropertiesMap[NodeTypeStorage]
// uses generic keys (storage_type/size/state) that GCP disks don't populate, so
// the useful native filter fields (disk_type/zone/disk_status) are Indexed here.
// disk_type/size_gb/zone are also the ontology's source keys (coalesce zone).
var persistentDiskSchema = core.SpecificTypeSchema{
	SpecificType: "PersistentDisk",
	NodeType:     core.NodeTypeStorage,
	Properties: []core.PropertyDef{
		{Name: "disk_type", Indexed: true},
		{Name: "zone", Indexed: true},
		{Name: "disk_status", Indexed: true},
		{Name: "size_gb"},
		{Name: "access_mode"},
		{Name: "attached_instances"},
	},
}

func init() { core.RegisterSpecificTypeSchema(persistentDiskSchema) }

// extractGCPPersistentDiskMetadata hoists key fields from the Disk's `meta`
// into top-level properties so phase-3 matchers and the attachment edge
// builder don't need to walk the raw meta map.
func (s *GCPSource) extractGCPPersistentDiskMetadata(properties map[string]interface{}, metaMap map[string]interface{}) {
	if sizeGb, ok := metaMap["size_gb"]; ok {
		properties["size_gb"] = sizeGb
	}
	if diskType, ok := metaMap["diskType"].(string); ok && diskType != "" {
		// diskType is a full self-link URL — strip to the short name to
		// match the convention used for zone above.
		properties["disk_type"] = extractGCPResourceNameFromURL(diskType)
	}
	if zone, ok := metaMap["zone"].(string); ok && zone != "" {
		properties["zone"] = extractGCPResourceNameFromURL(zone)
	}
	if accessMode, ok := metaMap["access_mode"].(string); ok && accessMode != "" {
		properties["access_mode"] = accessMode
	}
	if status, ok := metaMap["status"].(string); ok && status != "" {
		properties["disk_status"] = status
	}
	// `users` is a list of instance self-link URLs. Hoist the last-segment
	// instance names so the attachment edge builder can look them up by
	// name without re-walking meta. Type-switch handles both the
	// json.Unmarshal `[]interface{}` shape (production) and `[]string`
	// (potentially set by tests or future collector refactors).
	if usersRaw := metaMap["users"]; usersRaw != nil {
		var users []string
		switch val := usersRaw.(type) {
		case []string:
			for _, u := range val {
				if u != "" {
					users = append(users, extractGCPResourceNameFromURL(u))
				}
			}
		case []interface{}:
			for _, u := range val {
				if str, ok := u.(string); ok && str != "" {
					users = append(users, extractGCPResourceNameFromURL(str))
				}
			}
		}
		if len(users) > 0 {
			properties["attached_instances"] = users
		}
	}
}

// stringSliceFromProperty coerces a property whose runtime type may be
// []string or []interface{} (after JSON round-trip) into []string.
func stringSliceFromProperty(v interface{}) []string {
	if s, ok := v.([]string); ok {
		return s
	}
	anySlice, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(anySlice))
	for _, x := range anySlice {
		if str, ok := x.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

// createPersistentDiskAttachmentEdges emits Storage → HOSTED_ON → ComputeInstance
// for each entry in the disk's `users[]` (mirrors AWS createEBSEdges, aws_source.go:6010).
func (s *GCPSource) createPersistentDiskAttachmentEdges(lookup *sources.NodeLookup, req *core.SourceBuildRequest) []*core.DbEdge {
	edges := make([]*core.DbEdge, 0)

	storageNodes, ok := lookup.ByNodeType[core.NodeTypeStorage]
	if !ok {
		return edges
	}

	for _, disk := range storageNodes {
		if subtype, _ := disk.Properties["subtype"].(string); subtype != "PersistentDisk" {
			continue
		}
		for _, instanceName := range stringSliceFromProperty(disk.Properties["attached_instances"]) {
			instanceNode := findNodeByNameAndType(lookup, core.NodeTypeComputeInstance, instanceName)
			if instanceNode == nil {
				continue
			}
			edges = append(edges, s.createEdge(disk, instanceNode, core.RelationshipHostedOn,
				map[string]interface{}{"connection_type": "disk_attachment"}, req))
		}
	}

	s.logger.Info("created GCP persistent disk attachment edges", "edge_count", len(edges))
	return edges
}
