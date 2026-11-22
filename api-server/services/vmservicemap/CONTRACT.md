# Forager `vm_service_map` action — contract (proposed, v1)

The forager side is `vm_service_map` on the `http-proxy` of a Prometheus datasource
(nudgebee/forager, `pkg/proxy/http/vmservicemap.go`; documented in its `docs/proxy-modules.md`).
This file is what the server reads; change it and `testdata/` together with the forager.

## Why the forager builds it
The forager runs next to the customer's Prometheus-compatible store, queries the node-agent series
there, and returns a small aggregated answer, so the raw series never cross the relay.

## Request (`relay.ExecuteProxy`, action `vm_service_map`)
| param | type | meaning |
|---|---|---|
| `datasource_id` | string | forager datasource key of the account's Prometheus integration (`connection_mode=vm_agent`), resolved by the server |
| `start`, `end` | RFC3339 | the window. Every series below is evaluated over it |

## Response (`result` object, either wrapped as `{"result": {...}}` or bare)
```json
{
  "window": {"start": "2026-10-13T10:00:00Z", "end": "2026-10-13T10:15:00Z"},
  "hosts": [
    {"instance_id": "i-0aaa", "hostname": "orders-web-host", "ips": ["172.31.1.10"], "listeners": []}
  ],
  "services": [
    {"instance_id": "i-0aaa", "hostname": "orders-web-host", "container_id": "/system.slice/orders-web.service",
     "name": "orders-web.service", "kind": "systemd", "image": "", "application_types": ["nodejs"],
     "listeners": [{"addr": "0.0.0.0:80", "port": 80}]}
  ],
  "edges": [
    {"source_host": "orders-web-host", "source_instance_id": "i-0aaa",
     "source_service": "orders-web.service", "source_container_id": "/system.slice/orders-web.service",
     "destination": "172.31.1.149:8080", "destination_kind": "private", "destination_name": "",
     "destination_host": "orders-api-host", "destination_instance_id": "i-0bbb",
     "destination_service": "orders-api.service", "destination_container_id": "/system.slice/orders-api.service",
     "protocol": "http", "requests": 1200, "errors": 12, "latency_ms": 38.5,
     "bytes_sent": 482113, "bytes_received": 9120331}
  ]
}
```

### Semantics
- `hosts` are the node-agents that **reported in the window**, keyed in the store by the `instance`
  label (the agent's machine id): hostname from `node_info`, cloud instance id from `node_cloud_info`,
  `ips` from `node_net_interface_ip`. Only hosts reporting inside the window are listed, so an
  address reused after the window cannot match an old host.
- `services` are the **active** systemd units and containers on those hosts (`container_info`),
  keyed by the node-agent's container id: `/system.slice/<unit>.service` is `systemd`,
  `/docker/<name>` is `docker`, anything else `other`. Login sessions, slices and scopes are not
  services. `image` comes from `container_info`, `application_types` from
  `container_application_type`, `listeners` from `container_net_tcp_listen_info`.
- `edges` are caller-side (the agent sees outgoing requests of the services on its host), one per
  source service, destination and protocol.
- `destination_host` / `destination_service` are set by the forager when the destination address
  belongs to a reporting host: the host, and the service listening on that port there (an exact
  address match wins over a wildcard listener; a port several services share is left unresolved).
  A loopback destination resolves to the service listening locally. With a host but no service, the
  server shows an unknown process on that host.
- `destination_kind` is the node-agent's own value: `vm`, `private` (an address the agent could not
  name), `external`. `destination_name` is the name the agent resolved, or the destination's DNS
  name when it is one; empty when only the address is known.
- `protocol` is the application protocol the calls were seen on, or `tcp` for a destination with none.
- `requests`/`errors` are whole-number totals over the window; errors are HTTP 4xx/5xx, or `failed`
  for other protocols (the Kubernetes runner's verdict). `latency_ms` is the average per request.
- A service calling itself, and the name `localhost`, are not reported. `truncated` is set when
  there were more than 5000 edges.
- Not seen: requests from outside, or from a host without the agent (node-agent#369).

## Server-side resolution (per edge destination the forager did not place on a reporting host)
1. `private` / `vm` → "unknown internal host", by address.
2. otherwise → external service, by `destination_name` (its DNS name) or address when that is empty.

## Nodes
A service is a node named by its unit or container, in the namespace of its host, so the K8s map
components draw it as "service / host". Kinds: `SystemdService`, `DockerContainer`, `Process`,
`UnknownProcess` (a port with no known service on a reporting host), `UnknownInternalHost`,
`ExternalService`.
