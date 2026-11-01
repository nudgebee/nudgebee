# SNMP and Network-Device Discovery Assessment

This is the spike requested by [issue #383](https://github.com/nudgebee/nudgebee/issues/383), reviewed on 2026-10-31.

## Recommendation

Start from the existing forager discovery pipeline. Native SNMP fits as another inventory method beside SSH; LibreNMS may be an optional in-network source feeding the same pipeline. Confirm the network-device schema and first-release requirements before implementing either source, and validate topology access before promising graph edges.

## What “SNMP support” can mean

| Option | Nudgebee connects to | What it provides | Rough effort |
| --- | --- | --- | --- |
| Monitoring-system API | LibreNMS over HTTPS | Inventory and alert data LibreNMS already collects; SNMP credentials remain in LibreNMS | Medium |
| Native polling | Devices or a reachable agent over SNMP | Direct device discovery and polling, with Nudgebee owning credentials, polling, and OID mapping | High |
| Both | LibreNMS and devices | Wider coverage, with overlapping polling and competing sources to reconcile | High; defer |

LibreNMS’s stable, documented API is v0. Its documentation lists device, port, inventory, alert, ARP, switching, and routing endpoints and uses bearer-token authentication. The v1 API remains beta, is disabled by default, and currently documents only health and system endpoints. [API v0](https://docs.librenms.org/API/), [API v1 beta](https://docs.librenms.org/API/v1/)

The documented API can return devices, interfaces, inventory, and active alerts. LibreNMS’s own discovery modules also learn neighbors through LLDP, CDP, and FDP. The API overview does not document a dedicated neighbor/topology endpoint, so topology extraction needs a small live proof against the target LibreNMS version before it becomes a promised feature. [Devices](https://docs.librenms.org/API/Devices/), [Ports](https://docs.librenms.org/API/Ports/), [Alerts](https://docs.librenms.org/API/Alerts/), [discovery modules](https://docs.librenms.org/Support/Discovery%20Support/)

Native polling is technically viable in Go: GoSNMP supports SNMP v1/v2c/v3, walks, bulk walks, and traps. It does not provide MIB parsing, so Nudgebee would also own OID selection and vendor-specific normalization. Polling adds credential handling, UDP reachability, timeouts/retries, scheduling, concurrency limits, and deployment requirements for reaching customer networks. [GoSNMP](https://github.com/gosnmp/gosnmp), [LibreNMS SNMP configuration](https://docs.librenms.org/Support/Configuration/)

## Fit with Nudgebee

Network-device discovery already lives in **forager discovery** ([`vmpackage/`](../api-server/services/vmpackage/)): the in-network agent runs `discovery_sweep` within `allowed_cidrs`, SSH `discovery_inventory` uses versioned content packs, cron schedules scans, and `discovery_target` account links feed resource matching. Reachability, credential locality, scheduling, and account mapping therefore have an existing foundation; the effort estimates above describe the general polling work, not a requirement to build all of it again. Native SNMP should extend forager inventory, while an optional LibreNMS source should feed the same discovery → resource-match → knowledge-graph path. This needs agent-repository/content-pack work and agreed graph types for switches, routers, and interfaces, rather than a separate api-server inventory connector.

Normalized SNMP or LibreNMS inventory should retain the existing datasource's `own` / `discovery_target` account association through resource matching and graph ingestion. Graph nodes and edges record tenant and source provenance; their account field is named `CloudAccountID` in [`types.go`](../api-server/services/knowledge_graph/core/types.go). Keep device/interface identities stable within that scope. Define dedicated graph node types for physical devices (such as switches and routers) rather than overloading compute or cloud-resource types; reserve the existing `NodeTypeNetworkInterface` constant for interface entities. The required network-device schema is an explicit design decision, not a reason to introduce a parallel account-identity model.

## Follow-up scope

Before implementation, confirm:

- First-release entities: devices, interfaces, active alerts, metrics, and/or topology.
- How an optional forager LibreNMS source normalizes its read-only HTTPS results into the existing discovery pipeline; prove compatibility against the target deployment.
- Supported LibreNMS versions, expected inventory/alert refresh cadence, and whether alert acknowledgement is read-only or in scope.
- Whether neighbor data is available through the deployed API and has stable identifiers suitable for graph edges.

**Suggested first phase:** define the forager inventory and graph contract for network devices, then scope native SNMP and/or an optional read-only LibreNMS source against it. Keep alert writes and unproven topology edges outside that first implementation.
