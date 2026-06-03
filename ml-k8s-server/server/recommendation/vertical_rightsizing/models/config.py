from __future__ import annotations

import logging
from typing import Any, Optional, Union
import pydantic as pd
from server.recommendation.vertical_rightsizing.models.objects import KindLiteral

logger = logging.getLogger("krr")


class Config(pd.BaseModel):
    clusters: Union[str, None] = None
    namespaces: Union[list[str], None] = None
    resource_kinds: Union[list[KindLiteral], None] = None
    resource_names: Union[list[str], None] = None

    selector: Optional[str] = None

    # Value settings
    cpu_min_value: int = pd.Field(100, ge=0)  # in millicores
    memory_min_value: int = pd.Field(100, ge=0)  # in megabytes

    # Threading settings
    max_workers: int = pd.Field(2, ge=1)

    # Performance settings
    max_recommendations_per_batch: Optional[int] = pd.Field(None, ge=1)  # Optional limit - None means process all
    metrics_fetch_timeout: int = pd.Field(45, ge=5)  # Timeout for metrics fetching in seconds

    # Logging Settings
    strategy: str
    other_args: dict[str, Any]

    def __init__(self, **kwargs: Any) -> None:
        super().__init__(**kwargs)
