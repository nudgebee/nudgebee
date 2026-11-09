# sample_manual_declarations.json

Exerciser for manually declared Knowledge Graph nodes and dependencies
(#39830, #30989). One document declares both: paste it into **Import JSON** on
Knowledge Graph → Settings → **Manual Declarations**, or send it as `document`
to `kg_create_manual_declarations`.

## Before importing

Replace the placeholders with values from your tenant:

| Placeholder | Use | Where to find it |
|---|---|---|
| `<AWS account number>` | a connected AWS account | the 12-digit account number (the connected account's id also works) |
| `<vpc id>` | a VPC in that account | the `vpc-…` id or the VPC's name, as the graph shows it |
| `<K8s account id>` | a connected Kubernetes cluster | the cluster account's id (Kubernetes accounts have no account number) |
| `<a workload>`, `<its namespace>`, `<its cluster>` | a workload the graph already has | any Deployment the Knowledge Graph shows |

## The format

- `nodes[]` — each is a node to declare. `name` is required. In a connected
  account give `cloud_account` and `specific_type`, plus `region` for a cloud
  resource (and `network` for VPC-keyed types: EC2, load balancers, RDS,
  Redshift) or `namespace` for a Kubernetes one. Outside any connected account
  give `node_type`. `labels` is an object of strings. `properties` holds the
  optional properties of the type, in the shape the collector stores them:
  text, numbers and booleans as they are, a key/value property such as
  `annotations` as an object, a list such as `subnet_ids` as an array. Fields
  only the collector can know (timestamps, runtime state, allocated IPs,
  internal ids) are refused.
- `ref` names a node for the dependencies of the same document. It defaults to
  `name` and must be unique in the document.
- `dependencies[]` — `source` and `dest` are each either a **string**, the
  `ref` of a node in this document, or an **object** naming a node already in
  the graph by `node_type` + `name`, narrowed by `namespace`, `cluster`, `arn`,
  `account_id`. `relationship_type` is `CALLS` (the default), `PUBLISHES_TO`
  or `SUBSCRIBES_TO`.

The accepted `node_type` and `specific_type` values for your tenant are listed
under **View format** in the Import JSON dialog, which also offers a sample
built for your accounts.

A document that is not valid JSON, has a field the format does not know, uses
a `ref` twice, or names a `ref` no node carries is refused as a whole and
nothing is written. Everything else is reported item by item.

## Expected outcomes

`index` is the item's position in its array.

| Item | Expected | Why |
|---|---|---|
| nodes[0] `billing-db` | created | Tenant-scoped Database; `engine` is an allowed property outside accounts |
| nodes[1] `payments-api` | created | Tenant-scoped ExternalService |
| nodes[2] `orders-queue` | created | Keyed `aws:<account>:us-east-1:MessageQueue::orders-queue`, as the AWS collector keys it |
| nodes[3] `orders-db` | created | Keyed by the VPC's name; `existing` instead if that RDS instance is already collected |
| nodes[4] `ledger-worker` | created | Keyed `k8s:<cluster>::Workload:payments:ledger-worker`; `replicas` stored as the number 3 |
| nodes[5] `no-region` | rejected | `region is required` |
| nodes[6] `not-allowed` | rejected | `node type "Pod" cannot be declared` |
| nodes[7] `no-namespace` | rejected | `namespace is required` |
| dependencies[0..1], [4] | created, `resolved` | Both sides are nodes of the document, linked by node id |
| dependencies[2..3] | created | The document side is linked by node id; the other side resolves like any dependency (`resolved`, or unmatched / ambiguous) |
| dependencies[5] | rejected | `dest: node "no-region" was not declared: region is required` |

## Importing again

Importing the same document again changes nothing: every node that landed
comes back under `existing` with its node id, and every dependency that landed
comes back under `existing` with `manual dependency already declared`. A node the collector already
provides is `existing` too, and a dependency that names it by `ref` links to
the collected node.

## Calling the API

```bash
jq -n --slurpfile doc sample_manual_declarations.json \
  '{action: {name: "kg_create_manual_declarations"}, input: {request: {document: $doc[0]}}}'
```

Response: `{data: {nodes: {created, existing, rejected}, dependencies: {created, existing, rejected}}}`.
A document holds at most 500 nodes and dependencies together, and 1 MB.

## Cleaning up

**Delete all** in the Dependencies section, then in the Nodes section
(`kg_delete_all_manual_dependencies`, `kg_delete_all_manual_nodes`).
