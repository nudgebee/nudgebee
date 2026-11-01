# SNMP and Network-Device Discovery Assessment

This is the spike requested by [issue #383](https://github.com/nudgebee/nudgebee/issues/383), reviewed on 2026-10-31.

## Recommendation

Gather the first-release requirements, then scope a read-only LibreNMS API integration for devices, ports, and active alerts. Validate topology access against a real LibreNMS deployment before promising graph edges. Defer native SNMP polling until a concrete use case requires devices that are not already managed by a monitoring system.

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

Nudgebee already has account-scoped integration configuration in [`integration_config.go`](../api-server/services/integrations/core/integration_config.go) and registered graph sources in [`sources/registry.go`](../api-server/services/knowledge_graph/sources/registry.go). Graph nodes and edges record tenant and source provenance; their current account field is named `CloudAccountID` in [`types.go`](../api-server/services/knowledge_graph/core/types.go). A LibreNMS connector should use that source/provenance path and keep device/interface identities stable. It should not model network devices as cloud resource types. If topology is added later, define dedicated graph node types for physical devices (such as switches and routers) rather than overloading compute or cloud-resource types; reserve the existing NodeTypeNetworkInterface constant for interface entities. A generic monitoring-account identity may be needed if the existing cloud-account scope cannot represent a LibreNMS instance cleanly.

## Follow-up scope

Before implementation, confirm:

- First-release entities: devices, interfaces, active alerts, metrics, and/or topology.
- Whether the LibreNMS deployment is reachable from the Nudgebee service or requires an in-network agent. If private-network reachability is required, evaluate whether the existing account-scoped vm_agent/proxy path can route the read-only HTTPS API calls; confirm the deployment model rather than assuming compatibility.
- Supported LibreNMS versions, expected inventory/alert refresh cadence, and whether alert acknowledgement is read-only or in scope.
- Whether neighbor data is available through the deployed API and has stable identifiers suitable for graph edges.

**Suggested first phase:** read-only device, interface, and active-alert sync through LibreNMS API v0, with topology and native polling explicitly out of scope until those questions are answered.
