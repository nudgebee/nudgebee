import dataclasses
from datetime import datetime
from typing import Dict, Any, Optional

from apis.model.base_model import BaseDetails


@dataclasses.dataclass
class NodeDetails(BaseDetails):
    name: str
    cloud_resource_id: str
    is_active: bool
    node_creation_time: datetime
    memory_capacity: float
    cpu_capacity: float
    memory_allocatable: float
    cpu_allocatable: float
    meta: Dict[str, Any] = dataclasses.field(default_factory=dict)
    labels: Dict[str, Any] = dataclasses.field(default_factory=dict)
    conditions: Optional[str] = None
    taints: Optional[str] = None
    node_type: Optional[str] = None
    node_flavor: Optional[str] = None
    node_region: Optional[str] = None
    node_zone: Optional[str] = None
    external_ip: Optional[str] = None
    internal_ip: Optional[str] = None
    cost: Optional[float] = None
    memory_limits: Optional[int] = None
    cpu_limits: Optional[float] = None
