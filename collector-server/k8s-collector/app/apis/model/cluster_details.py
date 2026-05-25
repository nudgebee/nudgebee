from dataclasses import dataclass

from apis.model.base_model import BaseDetails


@dataclass
class ClusterDetails(BaseDetails):
    node_count: str
    spot_node_count: str
    ondemand_node_count: str
    pod_count: str
    workload_count: str
    running_pod_count: str
    pending_pod_count: str
    best_practice_score: str
    right_sizing_score: str
    region: str
    cluster_version: str
