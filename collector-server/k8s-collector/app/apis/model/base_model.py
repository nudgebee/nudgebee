from dataclasses import dataclass


@dataclass
class BaseDetails:
    tenant_id: str
    cloud_account_id: str
