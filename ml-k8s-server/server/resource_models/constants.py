from enum import Enum


class k8sResourceType(Enum):
    Pod = "Pod"
    DaemonSet = "DaemonSet"
    ReplicaSet = "ReplicaSet"
    # Job = "Job"
    StatefulSet = "StatefulSet"
    Deployment = "Deployment"
    Node = "Node"
    Rollout = "Rollout"
