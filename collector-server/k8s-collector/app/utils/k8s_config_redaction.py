"""Strip secret material from the ``config`` an agent sends for a workload or pod.

Discovery stores ``config`` as ``meta.config`` in k8s_workloads, k8s_pods and
cloud_resourses, which every role that can read Kubernetes resources, and the AI
tools, can read. Two parts of it can carry secrets:

* ``annotations``: ``kubectl apply`` keeps a full copy of the object in
  ``kubectl.kubernetes.io/last-applied-configuration``, inline env values
  included, and Rancher/k3s keeps a gzipped copy in
  ``objectset.rio.cattle.io/applied``. Both annotations are dropped. The current
  agent sends every annotation.
* ``containers[].env[].value``: older agents sent each container's env. A
  non-empty inline value becomes ``[redacted]``. The name and ``value_from`` are
  kept: ``value_from`` names the Secret or ConfigMap key, which is what the
  knowledge graph links a workload to its Secrets on.

Migration V1111 applies the same rule to rows already stored. Keep the two in step.
"""

from typing import Any

REDACTED_VALUE = "[redacted]"

APPLIED_COPY_ANNOTATIONS = frozenset(
    {
        "kubectl.kubernetes.io/last-applied-configuration",
        "objectset.rio.cattle.io/applied",
    }
)


def redact_k8s_config(config: Any) -> Any:
    """Return ``config`` without secret material. The input is never modified; a
    value that is not a dict is returned as it is."""
    if not isinstance(config, dict):
        return config
    out = dict(config)
    annotations = out.get("annotations")
    if isinstance(annotations, dict):
        out["annotations"] = {k: v for k, v in annotations.items() if k not in APPLIED_COPY_ANNOTATIONS}
    containers = out.get("containers")
    if isinstance(containers, list):
        out["containers"] = [_redact_container_env(c) for c in containers]
    return out


def _redact_container_env(container: Any) -> Any:
    if not isinstance(container, dict) or not isinstance(container.get("env"), list):
        return container
    env = []
    for entry in container["env"]:
        if (
            isinstance(entry, dict)
            and isinstance(entry.get("value"), str)
            and entry["value"] not in ("", REDACTED_VALUE)
        ):
            entry = {**entry, "value": REDACTED_VALUE}
        env.append(entry)
    return {**container, "env": env}
