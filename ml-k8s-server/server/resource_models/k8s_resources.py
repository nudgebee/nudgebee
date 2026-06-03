from typing import Any, Dict, List

from pydantic import BaseModel
from server.resource_models.resource import Resource
from server.resource_models.utils import convert_cpu_unit_to_cores, convert_memory_unit_to_bytes


class ResourceMeta(BaseModel):
    pass


class K8sResourceMeta(ResourceMeta):
    node: str | None
    config: Dict[str, Any]
    status: str | None
    namespace: str
    controllerKind: str
    total_pods: int


class K8sContainerResourcesLimitReq(BaseModel):
    memory: int | None
    cpu: float | None

    def __init__(self, **data: Any):
        # check if cpu value is valid
        data["memory"] = convert_memory_unit_to_bytes(data["memory"]) if "memory" in data else None
        data["cpu"] = convert_cpu_unit_to_cores(data["cpu"]) if "cpu" in data else None
        super().__init__(**data)


class K8sContainerResources(BaseModel):
    limits: K8sContainerResourcesLimitReq
    requests: K8sContainerResourcesLimitReq


class K8sContainer(BaseModel):
    name: str
    image: str
    resources: K8sContainerResources


class K8sResource(Resource):
    meta: K8sResourceMeta  # type: ignore
    namespace: str
    owner: str | None
    owner_kind: str | None
    containers: List[K8sContainer] = []
    annotations: Dict[str, str]
    labels: Dict[str, str]

    def __init__(self, **data: Any):
        meta_data: Dict[str, Any] = data.pop("meta", {})
        meta_instance: K8sResourceMeta = K8sResourceMeta(**dict(meta_data))
        config: Dict[str, Any] = meta_data.get("config", {})
        data["annotations"] = config.get("annotations") if config.get("annotations") else {}
        data["labels"] = config.get("labels") if config.get("labels") else {}
        data["meta"] = meta_instance
        data["namespace"] = meta_data.get("namespace")
        config = meta_data.get("config", {})
        owner_details = config.get("owner", {})
        data["owner"] = owner_details[0].get("name") if owner_details else None
        data["owner_kind"] = owner_details[0].get("kind") if owner_details else None
        if "containers" in config:
            data["containers"] = [K8sContainer(**i) for i in config["containers"]]
        super().__init__(**data)
