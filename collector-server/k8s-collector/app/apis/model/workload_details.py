from dataclasses import dataclass, field
from datetime import datetime
from typing import Dict, Any

from apis.model.base_model import BaseDetails


@dataclass
class WorkloadDetails(BaseDetails):
    cloud_resource_id: str
    external_id: str
    namespace: str
    is_active: bool
    name: str
    kind: str
    creation_time: datetime = datetime.now()
    last_seen: datetime = datetime.now()
    total_pods: int = 0
    ready_pods: int = 0
    labels: Dict[str, str] = field(default_factory=dict)
    meta: Dict[str, Any] = field(default_factory=dict)
