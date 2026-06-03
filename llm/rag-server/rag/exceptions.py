"""
Unified exceptions for RAG vector database operations.
"""


def is_not_found_error(error: Exception) -> bool:
    """
    Check if an error is a "not found" error from Qdrant.

    Args:
        error: The exception to check

    Returns:
        True if this is a not found error
    """
    error_msg = str(error).lower()
    error_type = type(error).__name__

    return "not found" in error_msg or "notfounderror" in error_type.lower() or "does not exist" in error_msg
