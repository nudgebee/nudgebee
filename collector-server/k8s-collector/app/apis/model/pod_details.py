from dataclasses import dataclass, field
from datetime import datetime
from typing import Dict, Any, Optional

from apis.model.base_model import BaseDetails
from utils.datetime_utils import utc_now


@dataclass
class PodDetails(BaseDetails):
    cloud_resource_id: str
    external_id: str
    name: str

    namespace: str
    status: str
    node_name: str
    is_active: bool = True
    workload_type: Optional[str] = None
    workload_name: Optional[str] = None
    restart_count: Dict[str, int] = field(default_factory=dict)
    creation_time: datetime = field(default_factory=utc_now)
    last_seen: datetime = field(default_factory=utc_now)
    labels: Dict[str, str] = field(default_factory=dict)
    meta: Dict[str, Any] = field(default_factory=dict)
