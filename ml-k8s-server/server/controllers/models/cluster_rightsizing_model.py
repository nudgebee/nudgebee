import dataclasses
from typing import List


@dataclasses.dataclass
class ClusterRightsizingRequest:
    region: str
    buffer_percentage: int
    min_nodes: int
    number_of_recommendations: int
    preferred_instance_groups: List[str]
    min_memory_per_node: int
    min_cpu_per_node: int
    graviton: bool


@dataclasses.dataclass
class InstanceTypeRecommendation:
    instance_types: List[str]
    region: str
    total_memory: int
    total_cpu: int
    cost: float
    number_of_nodes: int
    network_profile: List[str]
    reserved_memory: int
    reserved_cpu: int
    graviton: bool


@dataclasses.dataclass
class ClusterRightSizingResponse:
    current_instance_type: InstanceTypeRecommendation
    recommended_instance_type: List[InstanceTypeRecommendation]
