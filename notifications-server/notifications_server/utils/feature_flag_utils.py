import logging
from uuid import UUID

from sqlalchemy.orm import Session

from notifications_server.models.models import FeatureFlag

LOG = logging.getLogger(__name__)


def is_feature_enabled(
    session: Session,
    feature: str,
    tenant_id: str,
) -> bool:
    try:
        tenant_uuid = UUID(tenant_id) if isinstance(tenant_id, str) else tenant_id

        query = session.query(FeatureFlag).filter(
            FeatureFlag.feature_id == feature, FeatureFlag.tenant_id == tenant_uuid, FeatureFlag.status == "enabled"
        )

        tenant_flag = query.filter(FeatureFlag.account_id.is_(None)).first()
        if tenant_flag:
            LOG.debug(f"Feature flag '{feature}' found for tenant {tenant_id}: enabled")
            return True

        LOG.debug(f"Feature flag '{feature}' not found or not enabled for tenant {tenant_id}")
        return False

    except Exception as e:
        LOG.error(f"Error checking feature flag '{feature}' for tenant {tenant_id}: {e}", exc_info=True)
        return False
