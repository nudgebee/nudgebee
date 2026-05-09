import logging
import time
from collections import defaultdict

from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from sqlalchemy import create_engine, QueuePool, text

from utils.config import Config, DBConfig

logger = logging.getLogger(__name__)

# Create a thread-safe connection pool
engine = create_engine(DBConfig.url, poolclass=QueuePool, pool_size=5, max_overflow=20)

# Simple in-memory cache for integrations
_llm_integrations_cache: dict[str, dict] = {}
_embeddings_integrations_cache: dict[str, dict] = {}


def decrypt_value(encrypted_value: str) -> str:
    """
    Decrypt an encrypted value using AES-GCM.
    Matches the Go implementation in api-server/services/common/secrets.go
    """
    if not encrypted_value:
        return ""

    if not Config.nudgebee_encryption_key:
        logger.error("NUDGEBEE_ENCRYPTION_KEY not set, cannot decrypt values")
        return encrypted_value

    try:
        # Decode hex strings
        encrypted_data = bytes.fromhex(encrypted_value)
        key = bytes.fromhex(Config.nudgebee_encryption_key)

        # First 12 bytes are the nonce (IV), rest is ciphertext
        nonce = encrypted_data[:12]
        ciphertext = encrypted_data[12:]

        # Decrypt using AES-GCM
        aesgcm = AESGCM(key)
        plaintext = aesgcm.decrypt(nonce, ciphertext, None)

        return plaintext.decode("utf-8")
    except Exception as e:
        logger.error(f"Failed to decrypt value: {e}")
        return encrypted_value


def get_confluence_integrations(cloud_account_id):
    try:
        with engine.connect() as connection:
            query = text("""
                SELECT i.id, ica.cloud_account_id, icv.name, icv.value
                FROM integrations i
                JOIN integration_config_values icv
                ON i.id = icv.integration_id
                JOIN integrations_cloud_accounts ica
                ON i.id = ica.integration_id
                WHERE i.type = 'confluence'
                AND ica.cloud_account_id = :ac_id
                """)
            params = {"ac_id": cloud_account_id}
            result = connection.execute(query, params)

            integrations = defaultdict(dict)
            for row in result:
                integration_id = row.id
                if "integration_id" not in integrations[integration_id]:
                    integrations[integration_id]["integration_id"] = integration_id
                    integrations[integration_id]["account_id"] = row.cloud_account_id
                    integrations[integration_id]["config"] = {}

                integrations[integration_id]["config"][row.name] = row.value

            return list(integrations.values())

    except Exception as e:
        logger.exception("Error fetching confluence integrations: %s", e)
        return []


def get_servicenow_kb_integrations(cloud_account_id):
    """
    Fetch ServiceNow integrations with sync_knowledge_base enabled.
    ServiceNow integrations are tenant-level, so we fetch the tenant_id from the account
    and query integrations at tenant level.

    Args:
        cloud_account_id: The account ID to get tenant_id from

    Returns:
        List of dicts with integration_id and config.
        Password field is automatically decrypted.
    """
    try:
        with engine.connect() as connection:
            # First, get the tenant_id from the cloud account
            tenant_query = text("""
                SELECT tenant FROM cloud_accounts WHERE id = :ac_id
                """)
            tenant_result = connection.execute(tenant_query, {"ac_id": cloud_account_id})
            tenant_row = tenant_result.fetchone()

            if not tenant_row:
                logger.warning(f"No tenant found for account {cloud_account_id}")
                return []

            tenant_id = tenant_row.tenant

            # Query ServiceNow integrations at tenant level
            query = text("""
                SELECT i.id, icv.name, icv.value
                FROM integrations i
                JOIN integration_config_values icv ON i.id = icv.integration_id
                WHERE i.type = 'servicenow'
                AND i.tenant_id = :tenant_id
                AND i.status = 'enabled'
                """)
            result = connection.execute(query, {"tenant_id": tenant_id})

            integrations = defaultdict(dict)
            for row in result:
                integration_id = row.id
                if "integration_id" not in integrations[integration_id]:
                    integrations[integration_id]["integration_id"] = integration_id
                    integrations[integration_id]["config"] = {}

                # Decrypt password field (ServiceNow passwords are always encrypted)
                if row.name == "password":
                    integrations[integration_id]["config"][row.name] = decrypt_value(row.value)
                else:
                    integrations[integration_id]["config"][row.name] = row.value

            # Filter where sync_knowledge_base is enabled
            filtered = []
            for integration in integrations.values():
                sync_kb = integration.get("config", {}).get("sync_knowledge_base", "false")
                if sync_kb in ("true", "True", True, "1", 1):
                    filtered.append(integration)

            logger.info(f"Found {len(filtered)} ServiceNow KB integrations for tenant {tenant_id}")
            return filtered

    except Exception as e:
        logger.exception("Error fetching ServiceNow KB integrations: %s", e)
        return []


def get_llm_integrations(cloud_account_id):
    logger.info(f"Fetching LLM integrations for cloud_account_id={cloud_account_id}")
    now = time.time()
    cache_entry = _llm_integrations_cache.get(cloud_account_id)
    if cache_entry and now - cache_entry["time"] < Config.rag_llm_provider_cache_ttl:
        logger.info(f"Returning cached LLM integrations for cloud_account_id={cloud_account_id}")
        return cache_entry["data"]
    try:
        with engine.connect() as connection:
            query = text("""
                SELECT i.id, ica.cloud_account_id, icv.name, icv.value
                FROM integrations i
                JOIN integration_config_values icv
                ON i.id = icv.integration_id
                JOIN integrations_cloud_accounts ica
                ON i.id = ica.integration_id
                WHERE i.type = 'llm'
                AND ica.cloud_account_id = :ac_id
                """)
            params = {"ac_id": cloud_account_id}
            result = connection.execute(query, params)

            integrations = defaultdict(dict)
            for row in result:
                integration_id = row.id
                if "integration_id" not in integrations[integration_id]:
                    integrations[integration_id]["integration_id"] = integration_id
                    integrations[integration_id]["account_id"] = row.cloud_account_id
                    integrations[integration_id]["config"] = {}

                integrations[integration_id]["config"][row.name] = row.value

            data = list(integrations.values())
            _llm_integrations_cache[cloud_account_id] = {"data": data, "time": now}
            logger.info(f"Fetched {len(integrations)} LLM integrations for cloud_account_id={cloud_account_id}")
            return data

    except Exception as e:
        logger.exception("Error fetching llm integrations: %s", e)
        return []


def get_embeddings_integrations(cloud_account_id):
    logger.info(f"Fetching embeddings integrations for cloud_account_id={cloud_account_id}")
    now = time.time()
    cache_entry = _embeddings_integrations_cache.get(cloud_account_id)
    if cache_entry and now - cache_entry["time"] < Config.rag_llm_provider_cache_ttl:
        logger.info(f"Returning cached embeddings integrations for cloud_account_id={cloud_account_id}")
        return cache_entry["data"]
    try:
        with engine.connect() as connection:
            query = text("""
                SELECT i.id, ica.cloud_account_id, icv.name, icv.value
                FROM integrations i
                JOIN integration_config_values icv
                ON i.id = icv.integration_id
                JOIN integrations_cloud_accounts ica
                ON i.id = ica.integration_id
                WHERE i.type = 'embeddings'
                AND ica.cloud_account_id = :ac_id
                """)

            params = {"ac_id": cloud_account_id}
            result = connection.execute(query, params)

            integrations = defaultdict(dict)
            for row in result:
                integration_id = row.id
                if "integration_id" not in integrations[integration_id]:
                    integrations[integration_id]["integration_id"] = integration_id
                    integrations[integration_id]["account_id"] = row.cloud_account_id
                    integrations[integration_id]["config"] = {}

                integrations[integration_id]["config"][row.name] = row.value

            data = list(integrations.values())
            _embeddings_integrations_cache[cloud_account_id] = {"data": data, "time": now}
            return data

    except Exception as e:
        logger.exception("Error fetching embeddings integrations: %s", e)
        return []


# ---------------------------------------------------------------------------
# Tenant-scoped helpers
#
# ServiceNow and Confluence integrations are tenant-level resources — the
# source data is identical across every cloud account in a tenant, so
# ingesting once per tenant (not once per account) avoids N× duplicate
# scrapes and N× storage. These helpers support that pattern:
#
#   1. ``list_active_tenants`` — which tenants have at least one active K8s
#      cloud account (the existing activity signal used by scrapers).
#   2. ``get_*_integrations_for_tenant`` — integrations at the tenant level.
#   3. ``get_tenant_id_for_account`` / ``get_first_active_account_for_tenant``
#      — bridge functions for search-time lookups and for picking an account
#      whose credentials drive tenant-level embeddings.
# ---------------------------------------------------------------------------


def list_active_tenants():
    """Return a list of tenant IDs that have at least one active K8s cloud account.

    Activity signal matches ``get_active_accounts``: account must be active
    and paired with a CONNECTED agent of type ``kubernetes``.
    """
    try:
        with engine.connect() as connection:
            query = text(
                "SELECT DISTINCT ca.tenant FROM cloud_accounts ca "
                "INNER JOIN agent a ON ca.id = a.cloud_account_id "
                "WHERE ca.status = 'active' AND a.status = 'CONNECTED' "
                "AND ca.account_type = 'kubernetes' AND ca.tenant IS NOT NULL"
            )
            result = connection.execute(query)
            return [row.tenant for row in result]
    except Exception as e:
        logger.exception("Error listing active tenants: %s", e)
        return []


def get_tenant_id_for_account(cloud_account_id):
    """Resolve the tenant ID for a cloud account. Returns None if not found."""
    if not cloud_account_id:
        return None
    try:
        with engine.connect() as connection:
            query = text("SELECT tenant FROM cloud_accounts WHERE id = :ac_id")
            row = connection.execute(query, {"ac_id": cloud_account_id}).fetchone()
            return row.tenant if row else None
    except Exception as e:
        logger.exception("Error resolving tenant for account %s: %s", cloud_account_id, e)
        return None


def get_active_accounts_for_tenant(tenant_id):
    """Return all active K8s cloud accounts belonging to a tenant.

    Used by tenant-scoped scrapers to honour ``account_ids`` filters — the
    caller may target a specific account in the tenant, so we need the full
    set of accounts in the tenant to compute the intersection rather than
    picking an arbitrary one.
    """
    if not tenant_id:
        return []
    try:
        with engine.connect() as connection:
            query = text(
                "SELECT ca.id FROM cloud_accounts ca "
                "INNER JOIN agent a ON ca.id = a.cloud_account_id "
                "WHERE ca.status = 'active' AND a.status = 'CONNECTED' "
                "AND ca.account_type = 'kubernetes' AND ca.tenant = :tenant_id"
            )
            result = connection.execute(query, {"tenant_id": tenant_id})
            return [row.id for row in result]
    except Exception as e:
        logger.exception("Error fetching active accounts for tenant %s: %s", tenant_id, e)
        return []


def get_first_active_account_for_tenant(tenant_id):
    """Pick any one active cloud account in a tenant.

    Used to source embeddings / LLM-provider config for tenant-scoped scrapes
    without introducing a separate tenant-level embeddings API. Any account
    in the tenant works because LLM integrations are typically shared, and
    the embedding text is identical across accounts for tenant-scoped data.
    """
    if not tenant_id:
        return None
    try:
        with engine.connect() as connection:
            query = text(
                "SELECT ca.id FROM cloud_accounts ca "
                "INNER JOIN agent a ON ca.id = a.cloud_account_id "
                "WHERE ca.status = 'active' AND a.status = 'CONNECTED' "
                "AND ca.account_type = 'kubernetes' AND ca.tenant = :tenant_id "
                "LIMIT 1"
            )
            row = connection.execute(query, {"tenant_id": tenant_id}).fetchone()
            return row.id if row else None
    except Exception as e:
        logger.exception("Error fetching first account for tenant %s: %s", tenant_id, e)
        return None


def get_confluence_integrations_for_tenant(tenant_id):
    """Fetch Confluence integrations for an entire tenant.

    Joins through ``integrations_cloud_accounts`` → ``cloud_accounts`` so
    this works regardless of whether ``integrations.tenant_id`` is populated
    for the Confluence row (the column is populated for ServiceNow; Confluence
    uses the account pivot). Returns one entry per distinct integration.
    """
    if not tenant_id:
        return []
    try:
        with engine.connect() as connection:
            query = text("""
                SELECT DISTINCT i.id, icv.name, icv.value
                FROM integrations i
                JOIN integration_config_values icv ON i.id = icv.integration_id
                JOIN integrations_cloud_accounts ica ON i.id = ica.integration_id
                JOIN cloud_accounts ca ON ica.cloud_account_id = ca.id
                WHERE i.type = 'confluence' AND ca.tenant = :tenant_id
            """)
            result = connection.execute(query, {"tenant_id": tenant_id})

            integrations: dict = defaultdict(dict)
            for row in result:
                integration_id = row.id
                if "integration_id" not in integrations[integration_id]:
                    integrations[integration_id]["integration_id"] = integration_id
                    integrations[integration_id]["tenant_id"] = tenant_id
                    integrations[integration_id]["config"] = {}
                integrations[integration_id]["config"][row.name] = row.value

            return list(integrations.values())
    except Exception as e:
        logger.exception("Error fetching Confluence integrations for tenant %s: %s", tenant_id, e)
        return []


def get_servicenow_kb_integrations_for_tenant(tenant_id):
    """Fetch ServiceNow KB integrations for a tenant.

    Mirrors ``get_servicenow_kb_integrations`` but accepts tenant_id directly
    (no cloud_account → tenant lookup), and applies the same sync_knowledge_base
    gating + password decryption.
    """
    if not tenant_id:
        return []
    try:
        with engine.connect() as connection:
            query = text("""
                SELECT i.id, icv.name, icv.value
                FROM integrations i
                JOIN integration_config_values icv ON i.id = icv.integration_id
                WHERE i.type = 'servicenow'
                AND i.tenant_id = :tenant_id
                AND i.status = 'enabled'
            """)
            result = connection.execute(query, {"tenant_id": tenant_id})

            integrations: dict = defaultdict(dict)
            for row in result:
                integration_id = row.id
                if "integration_id" not in integrations[integration_id]:
                    integrations[integration_id]["integration_id"] = integration_id
                    integrations[integration_id]["tenant_id"] = tenant_id
                    integrations[integration_id]["config"] = {}
                if row.name == "password":
                    integrations[integration_id]["config"][row.name] = decrypt_value(row.value)
                else:
                    integrations[integration_id]["config"][row.name] = row.value

            filtered = []
            for integration in integrations.values():
                sync_kb = integration.get("config", {}).get("sync_knowledge_base", "false")
                if sync_kb in ("true", "True", True, "1", 1):
                    filtered.append(integration)
            logger.info(f"Found {len(filtered)} ServiceNow KB integrations for tenant {tenant_id}")
            return filtered
    except Exception as e:
        logger.exception("Error fetching ServiceNow KB integrations for tenant %s: %s", tenant_id, e)
        return []
