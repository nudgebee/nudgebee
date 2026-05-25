import json
import logging
import re
from datetime import datetime
from enum import Enum
from typing import Any, Dict, List, Optional, Tuple

import requests
from psycopg2 import sql

from config import Configs
from db import database


class RuleName(Enum):
    """Enum for rule names used in recommendations."""

    K8S_API_DELETED = "k8s_api_deleted"
    K8S_API_DEPRECATED = "k8s_api_deprecated"
    KUBE_PROXY_VERSION = "kube_proxy_version"
    EKS_ADD_ONS_VERSION = "eks_add_ons_version"
    EKS_CLUSTER_UPGRADE = "eks_cluster_upgrade"
    CLUSTER_UPGRADE_CONFIDENCE = "cluster_upgrade_confidence"


class Category(Enum):
    """Enum for recommendation categories."""

    INFRA_UPGRADE = "InfraUpgrade"


# Database operation constants
ON_CONFLICT_RECOMMENDATION = (
    "ON CONFLICT (cloud_account_id, rule_name, resource_id, category, "
    "account_object_id) DO UPDATE SET "
    "recommendation = EXCLUDED.recommendation, status=EXCLUDED.status"
)

DEFAULT_ESTIMATED_SAVINGS = 30 * 24 * 0.50  # 30 days extended support cost
VERSION_DIFFERENCE_THRESHOLD = 3
MAX_SKEW_THRESHOLD_VERSION = 25
EXTENDED_SUPPORT_HOURLY_RATE = 0.50


K8S_RESOURCE_TO_EKS_ADDON_MAPPING = {
    "coredns": "coredns",
    "ebs-csi-controller": "aws-ebs-csi-driver",
    "ebs-csi-node": "aws-ebs-csi-driver",
    "ebs-csi-node-windows": "aws-ebs-csi-driver",
    "aws-node": "vpc-cni",
}


def get_account_k8s_version(cloud_account_id: str, tenant: str) -> Optional[str]:
    """Get Kubernetes version for the specified account.

    Args:
        cloud_account_id: The cloud account identifier
        tenant: The tenant identifier

    Returns:
        Kubernetes version string (e.g., '1.25') or None if not found
    """
    select_sql = sql.SQL("select k8s_version from agent where cloud_account_id = %s and tenant = %s")
    rows = database.run_query(select_sql, [cloud_account_id, tenant])

    k8s_version_pattern = re.compile(r"1\.\d{2}")

    for row in rows:
        if row[0]:
            match = k8s_version_pattern.search(row[0])
            if match:
                return match.group()
    return None


def get_account_k8s_version_and_provider(cloud_account_id: str, tenant: str) -> Tuple[Optional[str], Optional[str]]:
    """Get Kubernetes version and provider for the specified account.

    Args:
        cloud_account_id: The cloud account identifier
        tenant: The tenant identifier

    Returns:
        Tuple of (k8s_version, k8s_provider) or (None, None) if not found
    """
    select_sql = sql.SQL("select k8s_version, k8s_provider from agent where cloud_account_id = %s and tenant = %s")
    rows = database.run_query(select_sql, [cloud_account_id, tenant])

    k8s_version_pattern = re.compile(r"1\.\d{2}")

    for row in rows:
        k8s_version, k8s_provider = row
        if k8s_version:
            match = k8s_version_pattern.search(k8s_version)
            if match:
                return match.group(), k8s_provider
    return None, None


def get_api_id(content: Dict[str, str]) -> str:
    """Generate unique API ID from content information.

    Args:
        content: Dictionary containing API information

    Returns:
        Unique API identifier string
    """
    return f"{content['deleted_version']}-{content['group']}+{content['kind']}/{content['version']}"


def _fetch_compatibility_matrix() -> Dict[str, Dict[str, List[str]]]:
    try:
        url = (
            "https://raw.githubusercontent.com/nudgebee/nudgebee-compatibility-registry/master/addon_compatibility.json"
        )
        response = requests.get(url, timeout=30)
        response.raise_for_status()

        compatibility_matrix = response.json()

        logging.info("Successfully fetched compatibility matrix with %d add-ons", len(compatibility_matrix))
        return compatibility_matrix

    except requests.RequestException as e:
        logging.error(f"Failed to fetch compatibility matrix from GitHub: {e}")
        return {}
    except json.JSONDecodeError as e:
        logging.error(f"Failed to parse compatibility matrix JSON: {e}")
        return {}
    except Exception as e:
        logging.error(f"Unexpected error fetching compatibility matrix: {e}")
        return {}


def _fetch_api_deprecation_registry() -> Dict[str, Any]:
    """Fetch Kubernetes API deprecation data from the nudgebee compatibility registry.

    Replaces the previous kubepug.xyz HTML scraping approach with structured JSON
    from our own registry, which is more reliable and maintained.

    Returns:
        Dictionary mapping K8s versions to their deprecated/deleted APIs
    """
    try:
        url = "https://raw.githubusercontent.com/nudgebee/nudgebee-compatibility-registry/master/api_deprecations.json"
        response = requests.get(url, timeout=30)
        response.raise_for_status()

        registry = response.json()
        logging.info("Successfully fetched API deprecation registry with %d versions", len(registry))
        return registry
    except requests.RequestException as e:
        logging.error(f"Failed to fetch API deprecation registry: {e}")
        return {}
    except json.JSONDecodeError as e:
        logging.error(f"Failed to parse API deprecation registry JSON: {e}")
        return {}
    except Exception as e:
        logging.error(f"Unexpected error fetching API deprecation registry: {e}")
        return {}


def _convert_registry_to_rows(registry: Dict[str, Any]) -> List[Dict[str, str]]:
    """Convert API deprecation registry data to the row format expected by existing recommendation logic.

    Args:
        registry: The deprecation registry keyed by K8s version

    Returns:
        List of row dicts compatible with _should_include_api_row and _create_api_recommendation_content
    """
    rows = []
    for version, data in registry.items():
        for api in data.get("deleted", []):
            row = {
                "Kind": api.get("kind", ""),
                "Group": api.get("group", ""),
                "Version": api.get("version", ""),
                "Deleted": api.get("deleted_version", version),
                "Deprecated": api.get("deprecated_version", ""),
                "Replacement": "",
            }
            if api.get("replacement_group") and api.get("replacement_version"):
                row["Replacement"] = (
                    f"{api['replacement_group']}/{api['replacement_version']}"
                    f"/{api.get('replacement_kind', api.get('kind', ''))}"
                )
            rows.append(row)

        for api in data.get("deprecated", []):
            row = {
                "Kind": api.get("kind", ""),
                "Group": api.get("group", ""),
                "Version": api.get("version", ""),
                "Deleted": api.get("deleted_version", ""),
                "Deprecated": api.get("deprecated_version", version),
                "Replacement": "",
            }
            if api.get("replacement_group") and api.get("replacement_version"):
                row["Replacement"] = (
                    f"{api['replacement_group']}/{api['replacement_version']}"
                    f"/{api.get('replacement_kind', api.get('kind', ''))}"
                )
            rows.append(row)

    return rows


def _fetch_kubepug_data() -> Tuple[List[str], List[Dict[str, str]]]:
    """Fetch Kubernetes API deprecation data from the compatibility registry.

    This function maintains backward compatibility with existing callers but
    now sources data from the nudgebee compatibility registry instead of
    scraping kubepug.xyz HTML.

    Returns:
        Tuple of (headers, rows) from the deprecation registry
    """
    registry = _fetch_api_deprecation_registry()
    if not registry:
        return [], []

    headers = ["Kind", "Group", "Version", "Deleted", "Deprecated", "Replacement"]
    rows = _convert_registry_to_rows(registry)
    return headers, rows


def _should_include_api_row(row: Dict[str, str], account_k8s_version: Optional[str]) -> bool:
    """Check if an API row should be included in recommendations.

    Args:
        row: Dictionary containing API row data
        account_k8s_version: Current Kubernetes version

    Returns:
        True if the API should be included in recommendations
    """
    if not row.get("Deleted") and not row.get("Deprecated"):
        return False

    if row.get("Deleted") and account_k8s_version and row["Deleted"] < account_k8s_version:
        return False

    return True


def _create_api_recommendation_content(row: Dict[str, str]) -> Dict[str, Any]:
    """Create recommendation content from API row data.

    Args:
        row: Dictionary containing API row data

    Returns:
        Dictionary containing recommendation content
    """
    content = {
        "kind": row["Kind"],
        "group": row["Group"],
        "version": row["Version"],
        "deleted_version": row["Deleted"],
        "deprecated_version": row["Deprecated"],
        "description": None,
        "replacement": {},
        "deleted_items": [],
        "deprecated_items": [],
    }

    if row.get("Replacement"):
        replacement_parts = row["Replacement"].split("/")
        if len(replacement_parts) >= 3:
            content["replacement"] = {
                "group": replacement_parts[0],
                "version": replacement_parts[1],
                "kind": replacement_parts[2],
            }

    return content


def add_deprecated_deleted_api_list(
    recommendations: List[Dict[str, Any]],
    cloud_account_id: str,
    tenant: str,
    rule: str,
) -> None:
    """Add deprecated/deleted API recommendations to the list.

    Args:
        recommendations: List to append recommendations to
        cloud_account_id: Cloud account identifier
        tenant: Tenant identifier
        rule: Rule name for the recommendation
    """
    account_k8s_version = get_account_k8s_version(cloud_account_id, tenant)
    _, rows = _fetch_kubepug_data()

    if not rows:
        logging.warning("No kubepug data available for API deprecation check")
        return

    for row in rows:
        if not _should_include_api_row(row, account_k8s_version):
            continue

        content = _create_api_recommendation_content(row)

        recommendation = {
            "cloud_account_id": cloud_account_id,
            "tenant_id": tenant,
            "resource_id": None,
            "recommendation": content,
            "recommendation_action": "Modify",
            "category": Category.INFRA_UPGRADE.value,
            "rule_name": rule,
            "severity": "Info",
            "account_object_id": get_api_id(content),
            "status": "Open",
        }
        recommendations.append(recommendation)


def _update_recommendation_with_report_data(
    recommendations: List[Dict[str, Any]], report_content: Dict[str, Any]
) -> None:
    """Update recommendations with data from the report.

    Args:
        recommendations: List of recommendations to update
        report_content: Content from the report to match against recommendations
    """
    for recommendation in recommendations:
        rec_data = recommendation["recommendation"]
        if (
            rec_data["kind"] == report_content["kind"]
            and rec_data["group"] == report_content["group"]
            and rec_data["version"] == report_content["version"]
        ):
            rec_data["severity"] = "High"
            rec_data["description"] = report_content["description"]
            rec_data["deleted_items"] = report_content.get("deleted_items", [])
            rec_data["deprecated_items"] = report_content.get("deprecated_items", [])
            break


def generate_k8s_upgrade_recommendations(cloud_account_id: str, report: Dict[str, Any], tenant: str) -> None:
    """Generate Kubernetes upgrade recommendations.

    Args:
        cloud_account_id: Cloud account identifier
        report: Report data containing upgrade information
        tenant: Tenant identifier
    """
    recommendations = []
    rule = "k8s_api_deprecated"
    add_deprecated_deleted_api_list(recommendations, cloud_account_id, tenant, rule)

    for row in report["data"]:
        content = row["content"][0]
        _update_recommendation_with_report_data(recommendations, content)

    try:
        archive_existing_recommendations_multi_rule(
            cloud_account_id,
            tenant,
            Category.INFRA_UPGRADE.value,
            [RuleName.K8S_API_DELETED.value, RuleName.K8S_API_DEPRECATED.value],
        )
        database.insert_data("recommendation", recommendations, on_conflict=ON_CONFLICT_RECOMMENDATION)
    except Exception as e:
        logging.exception(f"Failed to insert k8s upgrade recommendations: {e}")


def _calculate_severity_by_version_diff(version_diff: int) -> str:
    """Calculate severity based on version difference.

    Args:
        version_diff: Absolute difference between versions

    Returns:
        Severity level ('Low' or 'High')
    """
    return "Low" if version_diff < VERSION_DIFFERENCE_THRESHOLD else "High"


def check_version_skew(
    kp_minor: int,
    k8s_version: int,
    max_skew: int,
    kp_version: str,
    version_type: str = "current",
) -> Tuple[str, str]:
    """Check version skew between kube-proxy and Kubernetes API server.

    Args:
        kp_minor: Kube-proxy minor version number
        k8s_version: Kubernetes API server version number
        max_skew: Maximum allowed version skew
        kp_version: Full kube-proxy version string
        version_type: Type of version check ('current' or 'target')

    Returns:
        Tuple of (error_message, severity)
    """
    version_diff = abs(kp_minor - k8s_version)

    # Same versions - no issue
    if kp_minor == k8s_version:
        return (
            f"Kube-proxy version {kp_version} matches your {version_type} control plane version {k8s_version}. ",
            "Info",
        )

    severity = _calculate_severity_by_version_diff(version_diff)

    # Kube-proxy is newer than API server
    if kp_minor > k8s_version:
        error_message = f"kube-proxy v{kp_version} is newer than {version_type} v1.{k8s_version}. "
        return error_message, severity

    # Kube-proxy is too old
    elif kp_minor < (k8s_version - max_skew):
        error_message = (
            f"kube-proxy v{kp_version} is too old for {version_type} "
            f"control plane version v1.{k8s_version} "
            f"(must be within {max_skew} minor versions). "
        )
        return error_message, severity

    else:
        severity = "Medium"
        error_message = (
            f"kube-proxy v{kp_version} is behind your {version_type} control plane version v1.{k8s_version}."
            "Although it is supported and allowed, please review the version skew. "
        )
        return error_message, severity


def _get_max_skew(version: int) -> int:
    """Get maximum allowed skew based on Kubernetes version.

    Args:
        version: Kubernetes version number

    Returns:
        Maximum allowed skew
    """
    return 3 if version >= MAX_SKEW_THRESHOLD_VERSION else 2


def _combine_severities(current_severity: str, target_severity: str) -> str:
    """Combine two severity levels and return the higher one.

    Args:
        current_severity: Current version severity
        target_severity: Target version severity

    Returns:
        Combined severity level
    """
    if current_severity == "High" or target_severity == "High":
        return "High"
    return "Low"


def _parse_kube_proxy_version(kp_version: str) -> Optional[int]:
    """Parse kube-proxy version string to extract minor version.

    Args:
        kp_version: Full kube-proxy version string

    Returns:
        Minor version number or None if parsing fails
    """
    try:
        return int(kp_version.split(".")[1])
    except (ValueError, IndexError) as e:
        logging.warning(f"Invalid kube-proxy version {kp_version}. Error: {e}")
        return None


def check_version_skew_policy(
    cloud_account_id: str,
    recommendations: List[Dict[str, Any]],
    row: Dict[str, Any],
    tenant: str,
) -> None:
    """Check version skew policy and generate recommendations.

    Args:
        cloud_account_id: Cloud account identifier
        recommendations: List to append recommendations to
        row: Data row containing version information
        tenant: Tenant identifier
    """
    for content in row.get("content", []):
        kp_version = content.get("kube_proxy_version", "")
        control_plane_version = content.get("control_plane_version")

        if not kp_version or control_plane_version is None:
            logging.warning(
                "Skipping: Missing kube-proxy version or control plane version in data: %s",
                content,
            )
            continue

        # Convert current_version to int if it's a string
        if isinstance(control_plane_version, str):
            try:
                control_plane_version = int(control_plane_version)
            except ValueError:
                logging.warning(
                    "Invalid control plane version format: %s. Expected integer or string representation of integer.",
                    control_plane_version,
                )
                continue

        kube_proxy_minor = _parse_kube_proxy_version(kp_version)
        if kube_proxy_minor is None:
            continue

        # Check current version skew
        max_skew_current = _get_max_skew(control_plane_version)
        current_error, current_severity = check_version_skew(
            kube_proxy_minor,
            control_plane_version,
            max_skew_current,
            kp_version,
            "current",
        )

        # Check target version skew
        target_version = control_plane_version + 1
        max_skew_target = _get_max_skew(target_version)
        target_error, target_severity = check_version_skew(
            kube_proxy_minor, target_version, max_skew_target, kp_version, "target"
        )

        # Combine messages and determine final severity
        message = current_error + "\n" + target_error
        severity = _combine_severities(current_severity, target_severity)

        if message:  # Only generate recommendation if there are issues
            generate_recommendation(
                cloud_account_id,
                control_plane_version,
                message,
                kp_version,
                recommendations,
                target_version,
                tenant,
                severity,
            )


def generate_recommendation(
    cloud_account_id: str,
    current_version: int,
    message: str,
    kp_version: str,
    recommendations: List[Dict[str, Any]],
    target_version: int,
    tenant: str,
    severity: str = "Critical",
) -> None:
    """Generate a kube-proxy version recommendation.

    Args:
        cloud_account_id: Cloud account identifier
        current_version: Current Kubernetes version
        message: Recommendation message
        kp_version: Kube-proxy version
        recommendations: List to append recommendation to
        target_version: Target Kubernetes version
        tenant: Tenant identifier
        severity: Recommendation severity level
    """
    if not message:
        return

    recommendation_content = {
        "kube-proxy": kp_version,
        "current_k8s_version": current_version,
        "target_k8s_version": target_version,
        "message": message,
    }

    recommendation = {
        "cloud_account_id": cloud_account_id,
        "tenant_id": tenant,
        "resource_id": None,
        "recommendation": recommendation_content,
        "recommendation_action": "Modify",
        "category": Category.INFRA_UPGRADE.value,
        "rule_name": RuleName.KUBE_PROXY_VERSION.value,
        "severity": severity,
        "account_object_id": f"{kp_version}-{current_version}-{target_version}",
        "status": "Open",
    }
    recommendations.append(recommendation)


def generate_kube_proxy_version_recommendations(cloud_account_id: str, report: Dict[str, Any], tenant: str) -> None:
    """Generate kube-proxy version recommendations.

    Args:
        cloud_account_id: Cloud account identifier
        report: Report data containing version information
        tenant: Tenant identifier
    """
    recommendations = []

    for row in report.get("data", []):
        check_version_skew_policy(cloud_account_id, recommendations, row, tenant)

    if not recommendations:
        logging.info(
            "No kube-proxy version recommendations generated for account %s",
            cloud_account_id,
        )
        return

    try:
        archive_existing_with_rule(
            cloud_account_id,
            tenant,
            Category.INFRA_UPGRADE.value,
            RuleName.KUBE_PROXY_VERSION.value,
        )
        database.insert_data("recommendation", recommendations, on_conflict=ON_CONFLICT_RECOMMENDATION)
        logging.info(
            "Successfully inserted %d kube-proxy recommendations for account %s",
            len(recommendations),
            cloud_account_id,
        )
    except Exception as e:
        logging.exception(f"Failed to insert kube-proxy recommendations for account {cloud_account_id}: {e}")


def _create_addon_recommendation(
    cloud_account_id: str,
    tenant: str,
    add_on_type: str,
    version: str,
    supported_versions: List[str],
    cluster_version: str,
    target_version: str,
) -> Dict[str, Any]:
    """Create an EKS add-on recommendation.

    Args:
        cloud_account_id: Cloud account identifier
        tenant: Tenant identifier
        add_on_type: Type of add-on (e.g., 'coredns')
        version: Current version of the add-on
        supported_versions: List of supported versions
        cluster_version: Current cluster version
        target_version: Target cluster version

    Returns:
        Dictionary containing the recommendation
    """
    content = {
        "add_on_type": add_on_type,
        "version": version,
        "supported_versions": supported_versions,
        "target_k8s_version": target_version,
        "message": f"{add_on_type} version {version} is not compatible with k8s version {cluster_version}",
    }

    return {
        "cloud_account_id": cloud_account_id,
        "tenant_id": tenant,
        "resource_id": None,
        "recommendation": content,
        "recommendation_action": "Modify",
        "category": Category.INFRA_UPGRADE.value,
        "rule_name": RuleName.EKS_ADD_ONS_VERSION.value,
        "severity": "Critical",
        "account_object_id": f"{add_on_type}-{version}-{cluster_version}-{target_version}",
        "status": "Open",
    }


def _process_addon_compatibility(
    recommendations: List[Dict[str, Any]],
    cloud_account_id: str,
    tenant: str,
    add_on_type: str,
    version: str,
    compatibility_config: Dict[str, List[str]],
    cluster_version: str,
    target_version: str,
) -> None:
    """Process add-on compatibility and add recommendation if needed.

    Args:
        recommendations: List to append recommendations to
        cloud_account_id: Cloud account identifier
        tenant: Tenant identifier
        add_on_type: Type of add-on
        version: Current version of the add-on
        compatibility_config: Compatibility configuration mapping
        cluster_version: Current cluster version
        target_version: Target cluster version
    """
    if not version:
        return

    supported_versions = compatibility_config.get(cluster_version)
    if not supported_versions:
        logging.warning(f"No compatibility data for {add_on_type} on cluster version {cluster_version}")
        return

    if version.lstrip("v") not in [v.lstrip("v") for v in supported_versions]:
        recommendation = _create_addon_recommendation(
            cloud_account_id,
            tenant,
            add_on_type,
            version,
            supported_versions,
            cluster_version,
            target_version,
        )
        recommendations.append(recommendation)


def _is_version_compatible(addon_version: str, supported_versions: List[str]) -> bool:
    if not addon_version or not supported_versions:
        return False

    # Direct match first
    if addon_version in supported_versions:
        return True

    # Extract base version (remove suffixes like -eksbuild, -eks, etc.)
    def normalize_version(version: str) -> str:
        # Remove common suffixes
        base_version = version.split("-")[0]
        return base_version.strip()

    normalized_addon_version = normalize_version(addon_version)

    # Check against normalized supported versions
    for supported_version in supported_versions:
        normalized_supported = normalize_version(supported_version)
        if normalized_addon_version == normalized_supported:
            return True

    return False


def _check_addon_compatibility(
    eks_addon_name: str,
    addon_version: str,
    cluster_version: str,
    target_version: str,
    compatibility_matrix: Dict[str, Dict[str, List[str]]],
) -> Dict[str, Any]:
    # Initialize result
    result = {
        "upgrade_confidence": "unknown",
        "compatibility_issues": [],
        "current_compatible": False,
        "target_compatible": False,
    }

    # Get compatibility data for this add-on
    addon_compatibility = compatibility_matrix.get(eks_addon_name)
    if not addon_compatibility:
        # No compatibility data available
        result["upgrade_confidence"] = "unknown"
        result["compatibility_issues"].append(
            {
                "type": "no_compatibility_data",
                "message": f"No compatibility data available for {eks_addon_name}",
                "supported_versions": [],
                "recommended_action": "manual_review",
            }
        )
        return result

    # Clean version strings (remove 'v' prefix if present)
    clean_addon_version = addon_version.lstrip("v") if addon_version else ""

    # Check current version compatibility
    current_supported_versions = addon_compatibility.get(cluster_version, [])
    current_clean_versions = [v.lstrip("v") for v in current_supported_versions]
    result["current_compatible"] = _is_version_compatible(clean_addon_version, current_clean_versions)

    # Check target version compatibility
    target_supported_versions = addon_compatibility.get(target_version, [])
    target_clean_versions = [v.lstrip("v") for v in target_supported_versions]
    result["target_compatible"] = _is_version_compatible(clean_addon_version, target_clean_versions)

    # Determine upgrade confidence and issues
    if result["current_compatible"] and result["target_compatible"]:
        result["upgrade_confidence"] = "high"
    elif result["current_compatible"] and not result["target_compatible"]:
        result["upgrade_confidence"] = "low"
        result["compatibility_issues"].append(
            {
                "type": "target_incompatible",
                "message": f"{eks_addon_name} version {addon_version} "
                f"is not compatible with target Kubernetes version {target_version}",
                "supported_versions": target_supported_versions,
                "recommended_action": "upgrade_addon",
            }
        )
    elif not result["current_compatible"] and result["target_compatible"]:
        result["upgrade_confidence"] = "medium"
        result["compatibility_issues"].append(
            {
                "type": "current_incompatible",
                "message": f"{eks_addon_name} version {addon_version} "
                f"is not compatible with current Kubernetes version {cluster_version}",
                "supported_versions": current_supported_versions,
                "recommended_action": "upgrade_addon",
            }
        )
    else:
        result["upgrade_confidence"] = "low"
        result["compatibility_issues"].extend(
            [
                {
                    "type": "current_incompatible",
                    "message": f"{eks_addon_name} version {addon_version} "
                    f"is not compatible with current Kubernetes version {cluster_version}",
                    "supported_versions": current_supported_versions,
                    "recommended_action": "upgrade_addon",
                },
                {
                    "type": "target_incompatible",
                    "message": f"{eks_addon_name} version {addon_version} "
                    f"is not compatible with target Kubernetes version {target_version}",
                    "supported_versions": target_supported_versions,
                    "recommended_action": "upgrade_addon",
                },
            ]
        )

    return result


def _get_confidence_level(final_score: float) -> str:
    if final_score >= 80:
        return "high"
    elif final_score >= 60:
        return "medium"
    elif final_score >= 40:
        return "low"
    else:
        return "critical"


def _process_addon_confidence(addon: Dict[str, Any]) -> Tuple[str, str, int]:
    upgrade_confidence = addon.get("upgrade_confidence", "unknown")
    compatibility_issues = addon.get("compatibility_issues", [])

    critical_issues = 0
    if upgrade_confidence == "low":
        critical_issues = sum(1 for issue in compatibility_issues if issue.get("type") == "target_incompatible")

    if upgrade_confidence == "high":
        return "compatible", upgrade_confidence, critical_issues
    elif upgrade_confidence == "low":
        return "incompatible", upgrade_confidence, critical_issues
    else:
        return "unknown", upgrade_confidence, critical_issues


def _calculate_upgrade_confidence_score(addon_data_list: List[Dict[str, Any]]) -> Dict[str, Any]:
    """Calculate upgrade confidence score based on addon compatibility."""
    total_addons = len(addon_data_list)
    if total_addons == 0:
        return {
            "score": 100,
            "confidence_level": "high",
            "total_addons": 0,
            "compatible_addons": 0,
            "incompatible_addons": 0,
            "unknown_addons": 0,
        }

    compatible_count = 0
    incompatible_count = 0
    unknown_count = 0
    critical_issues = 0

    for addon in addon_data_list:
        confidence_category, _, addon_critical_issues = _process_addon_confidence(addon)

        if confidence_category == "compatible":
            compatible_count += 1
        elif confidence_category == "incompatible":
            incompatible_count += 1
            critical_issues += addon_critical_issues
        else:
            unknown_count += 1

    # Calculate confidence score (0-100)
    base_score = (compatible_count / total_addons) * 100
    critical_penalty = (critical_issues / total_addons) * 50  # Heavy penalty for critical issues
    unknown_penalty = (unknown_count / total_addons) * 20  # Moderate penalty for unknown

    final_score = max(0, base_score - critical_penalty - unknown_penalty)
    confidence_level = _get_confidence_level(final_score)

    return {
        "score": round(final_score, 1),
        "confidence_level": confidence_level,
        "total_addons": total_addons,
        "compatible_addons": compatible_count,
        "incompatible_addons": incompatible_count,
        "unknown_addons": unknown_count,
        "critical_issues": critical_issues,
    }


def _process_enhanced_addon_data(
    recommendations: List[Dict[str, Any]],
    cloud_account_id: str,
    tenant: str,
    addon_data: Dict[str, Any],
    cluster_version: str,
    target_version: str,
    compatibility_matrix: Dict[str, Dict[str, List[str]]],
    processed_images: set = None,
) -> None:
    addon_name = addon_data.get("name", "unknown")
    addon_version = addon_data.get("version", "")
    chart_name = addon_data.get("chart_name")
    image = addon_data.get("image", "")
    namespace = addon_data.get("namespace", "")
    workload_type = addon_data.get("workload_type", "")

    # Initialize processed_images set if not provided
    if processed_images is None:
        processed_images = set()

    # Filter: Only process if image name contains one of the required strings
    required_image_names = ["aws-ebs-csi-driver", "amazon-k8s-cni", "coredns"]
    if not any(required_name in image for required_name in required_image_names):
        return

    # Prevent duplicates: Check if this image has already been processed
    if image in processed_images:
        return

    # Add image to processed set
    processed_images.add(image)

    eks_addon_name = K8S_RESOURCE_TO_EKS_ADDON_MAPPING.get(addon_name, addon_name)

    # Calculate compatibility using the matrix instead of relying on robusta data
    compatibility_info = _check_addon_compatibility(
        eks_addon_name, addon_version, cluster_version, target_version, compatibility_matrix
    )

    upgrade_confidence = compatibility_info["upgrade_confidence"]
    compatibility_issues = compatibility_info["compatibility_issues"]

    # Store compatibility info in addon_data for confidence scoring
    addon_data["upgrade_confidence"] = upgrade_confidence
    addon_data["compatibility_issues"] = compatibility_issues

    content = {
        "addon_name": addon_name,
        "eks_addon_name": eks_addon_name,
        "chart_name": chart_name,
        "current_version": addon_version,
        "image": image,
        "namespace": namespace,
        "workload_type": workload_type,
        "cluster_version": cluster_version,
        "target_cluster_version": target_version,
        "upgrade_confidence": upgrade_confidence,
        "current_compatible": compatibility_info["current_compatible"],
        "target_compatible": compatibility_info["target_compatible"],
    }

    if compatibility_issues:
        for issue in compatibility_issues:
            severity = "Critical" if issue.get("type") == "target_incompatible" else "High"

            issue_content = content.copy()
            issue_content.update(
                {
                    "compatibility_issue": issue.get("message", ""),
                    "issue_type": issue.get("type", ""),
                    "supported_versions": issue.get("supported_versions", []),
                    "recommended_action": issue.get("recommended_action", "review_required"),
                }
            )

            recommendation = {
                "cloud_account_id": cloud_account_id,
                "tenant_id": tenant,
                "resource_id": None,
                "recommendation": issue_content,
                "recommendation_action": "Modify",
                "category": Category.INFRA_UPGRADE.value,
                "rule_name": RuleName.EKS_ADD_ONS_VERSION.value,
                "severity": severity,
                "account_object_id": f"{eks_addon_name}-{addon_name}-{addon_version}"
                f"-{cluster_version}-{target_version}",
                "status": "Open",
            }
            recommendations.append(recommendation)
    else:
        # Get supported versions from compatibility matrix
        addon_compatibility = compatibility_matrix.get(eks_addon_name, {})
        target_supported_versions = addon_compatibility.get(target_version, [])

        recommendations_copy = content.copy()
        recommendations_copy.update(
            {
                "supported_versions": target_supported_versions,
            }
        )

        # Create informational recommendation for all add-ons for inventory purposes
        recommendation = {
            "cloud_account_id": cloud_account_id,
            "tenant_id": tenant,
            "resource_id": None,
            "recommendation": content,
            "recommendation_action": "Modify",
            "category": Category.INFRA_UPGRADE.value,
            "rule_name": RuleName.EKS_ADD_ONS_VERSION.value,
            "severity": "Info",
            "account_object_id": f"{eks_addon_name}-{addon_name}-{addon_version}-inventory",
            "status": "Open",
        }
        recommendations.append(recommendation)


def _process_report_content(
    content: List[Dict[str, Any]],
    recommendations: List[Dict[str, Any]],
    cloud_account_id: str,
    tenant: str,
    cluster_version: str,
    target_version: str,
    compatibility_matrix: Dict[str, Dict[str, List[str]]],
    all_addon_data: List[Dict[str, Any]],
) -> None:
    """Process report content and generate addon recommendations."""
    if not content:
        return

    first_item = content[0]

    # Check if this is new enhanced addon data format
    if isinstance(first_item, dict) and any(key in first_item for key in ["name", "image", "workload_type"]):
        all_addon_data.extend(content)
        processed_images = set()  # Track processed images to prevent duplicates
        for addon_data in content:
            _process_enhanced_addon_data(
                recommendations,
                cloud_account_id,
                tenant,
                addon_data,
                cluster_version,
                target_version,
                compatibility_matrix,
                processed_images,
            )
    else:
        # Legacy format - process using old compatibility configs
        _process_legacy_addon_format(
            first_item,
            recommendations,
            cloud_account_id,
            tenant,
            cluster_version,
            target_version,
        )


def _process_legacy_addon_format(
    first_item: Dict[str, Any],
    recommendations: List[Dict[str, Any]],
    cloud_account_id: str,
    tenant: str,
    cluster_version: str,
    target_version: str,
) -> None:
    """Process legacy addon format using old compatibility configs."""
    addon_configs = {
        "coredns": Configs.CORE_DNS_COMPATIBILITY,
        "vpc-cni": Configs.VPC_CNI_COMPATIBILITY,
        "ebs-csi-driver": Configs.EBS_DRIVER_COMPATIBILITY,
    }

    for addon_key, config in addon_configs.items():
        addon_version = first_item.get(addon_key.replace("-", "_"), "")
        if addon_version:
            try:
                _process_addon_compatibility(
                    recommendations,
                    cloud_account_id,
                    tenant,
                    addon_key,
                    addon_version,
                    config,
                    cluster_version,
                    target_version,
                )
            except Exception as e:
                logging.warning(f"Failed to process {addon_key} compatibility for account {cloud_account_id}: {e}")


def _create_cluster_confidence_recommendation(
    cloud_account_id: str,
    tenant: str,
    cluster_version: str,
    target_version: str,
    confidence_metrics: Dict[str, Any],
) -> Dict[str, Any]:
    """Create cluster upgrade confidence recommendation."""
    return {
        "cloud_account_id": cloud_account_id,
        "tenant_id": tenant,
        "resource_id": None,
        "recommendation": {
            "upgrade_confidence_score": confidence_metrics["score"],
            "confidence_level": confidence_metrics["confidence_level"],
            "total_addons_analyzed": confidence_metrics["total_addons"],
            "compatible_addons": confidence_metrics["compatible_addons"],
            "incompatible_addons": confidence_metrics["incompatible_addons"],
            "unknown_addons": confidence_metrics["unknown_addons"],
            "critical_compatibility_issues": confidence_metrics["critical_issues"],
            "cluster_version": cluster_version,
            "target_cluster_version": target_version,
            "analysis_type": "gonogo_style_compatibility_check",
        },
        "recommendation_action": "Modify",
        "category": Category.INFRA_UPGRADE.value,
        "rule_name": RuleName.CLUSTER_UPGRADE_CONFIDENCE.value,
        "severity": "Info" if confidence_metrics["confidence_level"] in ["high", "medium"] else "High",
        "account_object_id": f"cluster-upgrade-confidence-{cluster_version}-{target_version}",
        "status": "Open",
    }


def _save_recommendations(
    recommendations: List[Dict[str, Any]],
    cloud_account_id: str,
    tenant: str,
) -> None:
    """Save recommendations to database."""
    if not recommendations:
        logging.info("No EKS add-on recommendations generated for account %s", cloud_account_id)
        return

    try:
        archive_existing_with_rule(
            cloud_account_id,
            tenant,
            Category.INFRA_UPGRADE.value,
            RuleName.EKS_ADD_ONS_VERSION.value,
        )
        database.insert_data("recommendation", recommendations, on_conflict=ON_CONFLICT_RECOMMENDATION)
        logging.info(
            "Successfully inserted %d EKS add-on recommendations for account %s",
            len(recommendations),
            cloud_account_id,
        )
    except Exception as e:
        logging.exception(f"Failed to insert EKS add-on recommendations for account {cloud_account_id}: {e}")


def generate_cluster_add_ons_recommendations(cloud_account_id: str, report: Dict[str, Any], tenant: str) -> None:
    """Generate add-on recommendations based on compatibility analysis."""
    recommendations = []
    cluster_version = get_account_k8s_version(cloud_account_id, tenant)

    if not cluster_version:
        logging.warning(f"No cluster version found for account {cloud_account_id}")
        return

    try:
        major, minor = map(int, cluster_version.split("."))
        target_version = f"{major}.{minor + 1}"
    except ValueError as e:
        logging.error(f"Invalid cluster version format: {cluster_version}. Error: {e}")
        return

    compatibility_matrix = _fetch_compatibility_matrix()
    if not compatibility_matrix:
        logging.warning(
            f"No compatibility matrix available for account {cloud_account_id}, proceeding without compatibility checks"
        )
        compatibility_matrix = {}

    all_addon_data = []

    # Process each row in report data
    for row in report["data"]:
        content = row.get("content", [])
        if isinstance(content, list):
            _process_report_content(
                content,
                recommendations,
                cloud_account_id,
                tenant,
                cluster_version,
                target_version,
                compatibility_matrix,
                all_addon_data,
            )

    # Generate cluster confidence recommendation if we have addon data
    if all_addon_data:
        confidence_metrics = _calculate_upgrade_confidence_score(all_addon_data)

        cluster_confidence_recommendation = _create_cluster_confidence_recommendation(
            cloud_account_id,
            tenant,
            cluster_version,
            target_version,
            confidence_metrics,
        )
        recommendations.append(cluster_confidence_recommendation)

        logging.info(
            "Calculated upgrade confidence for account %s: score=%.1f, level=%s, total_addons=%d",
            cloud_account_id,
            confidence_metrics["score"],
            confidence_metrics["confidence_level"],
            confidence_metrics["total_addons"],
        )

    # Save all recommendations to database
    _save_recommendations(recommendations, cloud_account_id, tenant)


def _calculate_support_costs(
    current_date, end_of_standard_support, end_of_extended_support
) -> Tuple[Optional[str], Optional[str], float]:
    """Calculate support costs and messages based on current date.

    Args:
        current_date: Current date
        end_of_standard_support: End of standard support date
        end_of_extended_support: End of extended support date

    Returns:
        Tuple of (upgrade_message, severity, cost_incurred)
    """
    if current_date > end_of_extended_support:
        upgrade_message = (
            "Critical: You are beyond extended support. Update is required! "
            "Cluster running after extended support period will be "
            "auto-upgraded to the next version by AWS."
        )
        severity = "Critical"
        hours_between_supports = (end_of_extended_support - end_of_standard_support).total_seconds() / 3600
        cost_incurred = hours_between_supports * EXTENDED_SUPPORT_HOURLY_RATE
        return upgrade_message, severity, cost_incurred

    if current_date > end_of_standard_support:
        upgrade_message = "Warning: You are on extended support for EKS Cluster, which incurs additional costs."
        severity = "High"
        hours_since_standard_support_ended = (current_date - end_of_standard_support).total_seconds() / 3600
        cost_incurred = hours_since_standard_support_ended * EXTENDED_SUPPORT_HOURLY_RATE
        return upgrade_message, severity, cost_incurred

    return None, None, 0.0


def _create_eks_recommendation(
    cloud_account_id: str,
    tenant: str,
    k8s_version: str,
    k8s_provider: str,
    upgrade_message: str,
    version_info: Dict[str, Any],
    cost_incurred: float,
    severity: str,
    account_name: str,
) -> Dict[str, Any]:
    """Create EKS cluster upgrade recommendation.

    Args:
        cloud_account_id: Cloud account identifier
        tenant: Tenant identifier
        k8s_version: Kubernetes version
        k8s_provider: Kubernetes provider
        upgrade_message: Upgrade message
        version_info: Version support information
        cost_incurred: Additional cost incurred
        severity: Recommendation severity
        account_name: Account name

    Returns:
        Dictionary containing the recommendation
    """
    return {
        "cloud_account_id": cloud_account_id,
        "tenant_id": tenant,
        "resource_id": None,
        "recommendation": json.dumps(
            {
                "eks_version": k8s_version,
                "message": upgrade_message or "Upgrade recommended for improved security and stability.",
                "end_of_support": version_info,
                "additional_cost_incurred": round(cost_incurred, 2),
            }
        ),
        "recommendation_action": "Modify",
        "category": Category.INFRA_UPGRADE.value,
        "rule_name": RuleName.EKS_CLUSTER_UPGRADE.value,
        "estimated_savings": DEFAULT_ESTIMATED_SAVINGS,
        "severity": severity,
        "account_object_id": f"{k8s_version}-{k8s_provider}-{severity}-{account_name}",
        "status": "Open",
    }


def generate_eks_cluster_upgrade_recommendation(tenant: str, cloud_account_id: str, account_name: str) -> None:
    """Generate EKS cluster upgrade recommendation.

    Args:
        tenant: Tenant identifier
        cloud_account_id: Cloud account identifier
        account_name: Account name (used for object ID)
    """
    logging.info(
        "Processing eks_cluster_upgrade for tenant %s, account id %s",
        tenant,
        cloud_account_id,
    )

    k8s_version, k8s_provider = get_account_k8s_version_and_provider(cloud_account_id, tenant)
    if not k8s_version or k8s_provider != "EKS":
        logging.info("Skipping EKS upgrade check - not an EKS cluster or version not found")
        return

    k8s_version = k8s_version.lstrip("v")
    version_info = Configs.EKS_VERSIONS_SUPPORT.get(k8s_version)

    if not version_info:
        logging.warning(f"Unknown EKS version {k8s_version} for account {cloud_account_id}")
        return

    try:
        current_date = datetime.now().date()
        end_of_standard_support = datetime.strptime(version_info["end_of_standard_support"], "%Y-%m-%d").date()
        end_of_extended_support = datetime.strptime(version_info["end_of_extended_support"], "%Y-%m-%d").date()
    except (KeyError, ValueError) as e:
        logging.error(f"Invalid date format in version info for {k8s_version}: {e}")
        return

    upgrade_message, severity, cost_incurred = _calculate_support_costs(
        current_date, end_of_standard_support, end_of_extended_support
    )

    if not severity:  # No recommendation needed if still in standard support
        logging.info("EKS cluster %s is within standard support period", cloud_account_id)
        return

    recommendation = _create_eks_recommendation(
        cloud_account_id,
        tenant,
        k8s_version,
        k8s_provider,
        upgrade_message,
        version_info,
        cost_incurred,
        severity,
        account_name,
    )
    recommendations = [recommendation]

    try:
        archive_existing_with_rule(
            cloud_account_id,
            tenant,
            Category.INFRA_UPGRADE.value,
            RuleName.EKS_CLUSTER_UPGRADE.value,
        )
        database.insert_data("recommendation", recommendations, on_conflict=ON_CONFLICT_RECOMMENDATION)
        logging.info(
            "Successfully created EKS upgrade recommendation for account %s",
            cloud_account_id,
        )
    except Exception as e:
        logging.exception(f"Failed to insert EKS upgrade recommendation for account {cloud_account_id}: {e}")


def archive_existing_recommendations_multi_rule(cloud_account_id, tenant, category, rules):
    """Archive existing recommendations for multiple rules."""
    update_to_archive = (
        "update recommendation set status = %s where tenant_id = %s and "
        "cloud_account_id = %s and category = %s and rule_name = ANY(%s) "
        "and status not in ('Archive')"
    )
    database.run_query(update_to_archive, ["Archive", tenant, cloud_account_id, category, rules])


def archive_existing_with_rule(cloud_account_id, tenant, category, rule_name):
    """Archive existing recommendations for a specific rule."""
    update_to_archive = (
        "update recommendation set status = %s where tenant_id = %s and "
        "cloud_account_id = %s and category = %s and rule_name = %s"
    )
    database.run_query(update_to_archive, ["Archive", tenant, cloud_account_id, category, rule_name])


def handle_k8s_version_upgrade_report(
    content: Dict[str, Any], tenant: str, cloud_account_id: str, account_name: str
) -> None:
    """Handle Kubernetes version upgrade report processing.

    Args:
        content: Report content containing evidence data
        tenant: Tenant identifier
        cloud_account_id: Cloud account identifier
        account_name: Account name (unused but kept for compatibility)
    """
    logging.info(
        "Processing k8s_version_upgrade report for tenant '%s', account id '%s'",
        tenant,
        cloud_account_id,
    )
    logging.debug("Account name '%s'", account_name)

    scan_type_handlers = {
        "k8s_version_upgrade": generate_k8s_upgrade_recommendations,
        "kube_proxy_version_check": generate_kube_proxy_version_recommendations,
        "cluster_add_on_version_check": generate_cluster_add_ons_recommendations,
    }

    for evidence in content.get("evidence", []):
        try:
            report = json.loads(evidence.get("data", "[]"))
            if not report:
                logging.warning("Empty report data for account %s", cloud_account_id)
                continue

            report_data = report[0]
            scan_type = report_data.get("scan_type")

            if not scan_type:
                logging.warning(
                    "No scan_type found in report data for account %s",
                    cloud_account_id,
                )
                continue

            handler = scan_type_handlers.get(scan_type)
            if handler:
                handler(cloud_account_id, report_data, tenant)
            else:
                logging.warning("Unknown scan type: %s for account %s", scan_type, cloud_account_id)

        except (json.JSONDecodeError, IndexError, KeyError) as e:
            logging.error("Invalid report data format for account %s: %s", cloud_account_id, e)
        except Exception as e:
            logging.exception("Failed to process report for account %s: %s", cloud_account_id, e)
