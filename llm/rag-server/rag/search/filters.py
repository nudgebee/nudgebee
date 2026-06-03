"""
Qdrant metadata filter builder utilities.

Provides helper functions for constructing Qdrant Filter objects
for metadata-based filtering during vector search.

Example usage:
    # Simple filter
    filter = build_metadata_filter(module="support", account="123")

    # Complex filter with conditions
    filter = Filter(
        must=[
            FieldCondition(key="metadata.module", match=MatchValue(value="support")),
            FieldCondition(key="metadata.account", match=MatchValue(value="123"))
        ]
    )

    # Range filter
    filter = build_range_filter("metadata.timestamp", gte=start_time, lt=end_time)
"""

import logging
from typing import Any, Dict, List, Optional, Union, cast

from qdrant_client.models import (
    Filter,
    FieldCondition,
    MatchValue,
    MatchAny,
    Range,
)

logger = logging.getLogger(__name__)


def build_metadata_filter(
    module: Optional[str] = None,
    account: Optional[str] = None,
    source: Optional[str] = None,
    custom_fields: Optional[Dict[str, Any]] = None,
) -> Optional[Filter]:
    """
    Build a Qdrant Filter object for common metadata fields.

    Args:
        module: Filter by module name (e.g., "support", "billing")
        account: Filter by account ID
        source: Filter by source (e.g., "docs.example.com")
        custom_fields: Additional custom metadata fields to filter

    Returns:
        Qdrant Filter object or None if no filters provided

    Example:
        filter = build_metadata_filter(module="support", account="123")
        results = client.search(
            collection_name="my_collection",
            query_vector=vector,
            query_filter=filter
        )
    """
    conditions = []

    # Module filter
    if module is not None:
        conditions.append(FieldCondition(key="metadata.module", match=MatchValue(value=module)))

    # Account filter (match specific account OR global)
    if account is not None:
        # Allow both specific account and "global" account
        conditions.append(FieldCondition(key="metadata.account", match=MatchAny(any=[account, "global"])))

    # Source filter
    if source is not None:
        conditions.append(FieldCondition(key="metadata.source", match=MatchValue(value=source)))

    # Custom fields
    if custom_fields:
        for key, value in custom_fields.items():
            # Add metadata. prefix if not already present
            field_key = f"metadata.{key}" if not key.startswith("metadata.") else key

            if isinstance(value, list):
                # Match any value in list
                conditions.append(FieldCondition(key=field_key, match=MatchAny(any=value)))
            else:
                # Match exact value
                conditions.append(FieldCondition(key=field_key, match=MatchValue(value=value)))

    # Return None if no filters (Qdrant will return all results)
    if not conditions:
        return None

    # Build Filter with all conditions as "must" (AND logic)
    return Filter(must=cast(List[Any], conditions))


def build_range_filter(
    field: str,
    gte: Optional[Union[int, float]] = None,
    gt: Optional[Union[int, float]] = None,
    lte: Optional[Union[int, float]] = None,
    lt: Optional[Union[int, float]] = None,
) -> Filter:
    """
    Build a range filter for numeric metadata fields.

    Args:
        field: Metadata field name (e.g., "metadata.timestamp")
        gte: Greater than or equal to
        gt: Greater than
        lte: Less than or equal to
        lt: Less than

    Returns:
        Qdrant Filter object

    Example:
        # Get documents from last 7 days
        import time
        week_ago = time.time() - (7 * 24 * 60 * 60)
        filter = build_range_filter("metadata.timestamp", gte=week_ago)
    """
    # Add metadata. prefix if not already present
    field_key = f"metadata.{field}" if not field.startswith("metadata.") else field

    range_obj = Range(gte=gte, gt=gt, lte=lte, lt=lt)

    return Filter(must=[FieldCondition(key=field_key, range=range_obj)])


def combine_filters(
    filters: List[Filter],
    logic: str = "must",
) -> Filter:
    """
    Combine multiple filters with AND or OR logic.

    Args:
        filters: List of Filter objects to combine
        logic: "must" (AND) or "should" (OR)

    Returns:
        Combined Filter object

    Example:
        # Search for (module=support AND account=123) OR (module=billing AND account=456)
        filter1 = build_metadata_filter(module="support", account="123")
        filter2 = build_metadata_filter(module="billing", account="456")
        combined = combine_filters([filter1, filter2], logic="should")
    """
    if not filters:
        return Filter()

    if len(filters) == 1:
        return filters[0]

    # Combine all conditions from all filters
    all_conditions: List[Any] = []
    for f in filters:
        if f.must:
            all_conditions.extend(f.must)

    if logic == "must":
        return Filter(must=all_conditions)
    elif logic == "should":
        return Filter(should=all_conditions)
    else:
        raise ValueError(f"Invalid logic '{logic}'. Use 'must' (AND) or 'should' (OR)")


def build_filter_from_dict(filter_dict: Dict[str, Any]) -> Optional[Filter]:
    """
    Build a Qdrant Filter from a dictionary specification.

    Supports both simple and complex filter definitions.

    Args:
        filter_dict: Filter specification as dictionary

    Returns:
        Qdrant Filter object or None

    Example simple usage:
        filter_dict = {"module": "support", "account": "123"}
        filter = build_filter_from_dict(filter_dict)

    Example complex usage:
        filter_dict = {
            "must": [
                {"field": "module", "value": "support"},
                {"field": "account", "values": ["123", "global"]}  # Match any
            ],
            "should": [
                {"field": "priority", "value": "high"},
                {"field": "priority", "value": "critical"}
            ]
        }
        filter = build_filter_from_dict(filter_dict)

    Example range usage:
        filter_dict = {
            "must": [
                {"field": "timestamp", "gte": 1234567890, "lt": 1234567999}
            ]
        }
    """
    if not filter_dict:
        return None

    # Simple case: flat dictionary of field:value pairs
    if "must" not in filter_dict and "should" not in filter_dict:
        return build_metadata_filter(
            module=filter_dict.get("module"),
            account=filter_dict.get("account"),
            source=filter_dict.get("source"),
            custom_fields={k: v for k, v in filter_dict.items() if k not in ["module", "account", "source"]},
        )

    # Complex case: explicit must/should conditions
    must_conditions: List[Any] = []
    should_conditions: List[Any] = []

    for condition_dict in filter_dict.get("must", []):
        cond = _build_condition_from_dict(condition_dict)
        if cond:
            must_conditions.append(cond)

    for condition_dict in filter_dict.get("should", []):
        cond = _build_condition_from_dict(condition_dict)
        if cond:
            should_conditions.append(cond)

    must_not_conditions: List[Any] = []
    for condition_dict in filter_dict.get("must_not", []):
        cond = _build_condition_from_dict(condition_dict)
        if cond:
            must_not_conditions.append(cond)

    return Filter(
        must=must_conditions or None,
        should=should_conditions or None,
        must_not=must_not_conditions or None,
    )


def _build_condition_from_dict(cond_dict: Dict[str, Any]) -> Optional[FieldCondition]:
    """
    Build a single FieldCondition from dictionary.

    Internal helper for build_filter_from_dict.
    """
    field = cond_dict.get("field")
    if not field:
        logger.warning(f"Filter condition missing 'field': {cond_dict}")
        return None

    # Add metadata. prefix if not present
    field_key = f"metadata.{field}" if not field.startswith("metadata.") else field

    # Match single value
    if "value" in cond_dict:
        return FieldCondition(key=field_key, match=MatchValue(value=cond_dict["value"]))

    # Match any of multiple values
    if "values" in cond_dict:
        return FieldCondition(key=field_key, match=MatchAny(any=cond_dict["values"]))

    # Range filter
    if any(k in cond_dict for k in ["gte", "gt", "lte", "lt"]):
        range_obj = Range(
            gte=cond_dict.get("gte"),
            gt=cond_dict.get("gt"),
            lte=cond_dict.get("lte"),
            lt=cond_dict.get("lt"),
        )
        return FieldCondition(key=field_key, range=range_obj)

    logger.warning(f"Filter condition has no value/values/range: {cond_dict}")
    return None
