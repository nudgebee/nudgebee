# sample_manual_nodes.csv

Exerciser for manually declared Knowledge Graph nodes (#39830). Drop it into
the **Import CSV** dialog on Knowledge Graph → Settings → **Manual Nodes**, or
POST the body to `kg_import_manual_nodes`.

## Before importing

Replace the placeholders with values from your tenant, or those rows are
rejected with `no active cloud account`:

| Placeholder | Use | Where to find it |
|---|---|---|
| `<AWS account number>` | a connected AWS account | the 12-digit account number (the connected account's id also works) |
| `<vpc id>` | a VPC in that account | the `vpc-…` id or the VPC's name, as the graph shows it |
| `<K8s account id>` | a connected Kubernetes cluster | the cluster account's id (Kubernetes accounts have no account number) |

Columns: `name` is required. The accepted `node_type` and `specific_type`
values for your tenant, with the identity columns each one needs, are listed
under **View full schema** in the Import CSV dialog. In a connected account a row needs
`specific_type` and, for a cloud resource, `region` (plus `network` for VPC-keyed
types: EC2, load balancers, RDS, Redshift); a Kubernetes resource needs
`namespace`. Outside any connected account a row needs `node_type`. `labels`
is `key=value;key=value`. Any other column (`engine`, `annotations` here) is an
optional property of the row's type, written in the shape the collector stores
it in: a key/value property such as `annotations` like labels, a list such as
`subnet_ids` as `a;b`. Fields only the collector can know (timestamps, runtime
state, allocated IPs and ports, internal ids) are refused with `… is read from
the resource when it is collected`.

## Expected outcomes

`row_index` is zero-based and counts data rows only.

| Row | `row_index` | Expected | Why |
|---|---|---|---|
| A1 | 0 | imported | Tenant-scoped Database; `engine` is an allowed property outside accounts |
| A2 | 1 | imported | Tenant-scoped ExternalService |
| B1 | 2 | imported | Keyed `aws:<account>:us-east-1:MessageQueue::orders-queue`, as the AWS collector keys it |
| B2 | 3 | imported | Keyed by the VPC's name; refused instead if that RDS instance is already collected |
| B3 | 4 | imported | Keyed `k8s:<cluster>::Workload:payments:ledger-worker`; `annotations` stored as a key/value map |
| B4 | 5 | imported | Keyed `k8s:<cluster>::K8sService:payments:ledger-svc` |
| C1 | 6 | rejected | `region is required` |
| C2 | 7 | rejected | `node type "Pod" cannot be declared` |
| C3 | 8 | rejected | `namespace is required` |
| C4 | 9 | rejected | `node type "Workload" cannot be declared` (only in a cluster account) |

Any row naming a resource the collector already provides is rejected with
`this resource is already in the graph (node …)` — declare dependencies against
that node instead.

## Cleaning up

**Delete all** on the Manual Nodes tab (or `kg_delete_all_manual_nodes`)
withdraws every declaration and removes each node that is still only declared.
