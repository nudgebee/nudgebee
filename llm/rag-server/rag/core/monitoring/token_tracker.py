import logging
from typing import Optional

from sqlalchemy import text

from rag.core.utils.db_query import engine

logger = logging.getLogger(__name__)


def persist_llm_token_usage(
    conversation_id: str,
    message_id: str,
    agent_name: str,
    account_id: str,
    token_usage: dict,
    user_id: Optional[str] = None,
    agent_id: Optional[str] = None,
) -> None:
    """
    Log LLM token usage to llm_conversation_token_usage table.

    Args:
        conversation_id: UUID of the conversation (required)
        message_id: UUID of the message (required)
        agent_name: Name of the agent (required)
        account_id: UUID of the account (required)
        token_usage: Dictionary containing token usage metrics:
            - llm_provider: Name of the LLM provider
            - llm_model: Name of the LLM model
            - input_tokens: Number of input tokens
            - output_tokens: Number of output tokens
            - latency_seconds: Request latency in seconds
            - request_status: Status of the request (success/failure)
            - error_message: Error message if request failed
            - content_length: Length of the response content
        user_id: UUID of the user (optional)
        agent_id: UUID of the agent (optional)
    """
    try:
        # Adding rag to agent name to distinguish toke usage
        agent_name = "rag_" + agent_name
        with engine.connect() as connection:
            with connection.begin():
                insert_query = text("""
                    INSERT INTO llm_conversation_token_usage (
                        conversation_id, message_id, agent_id, agent_name, account_id, user_id,
                        llm_provider, llm_model,
                        input_tokens, output_tokens, cached_input_tokens, cache_creation_tokens,
                        is_cache_hit, cache_hit_rate,
                        retry_attempt, fallback_from_model, fallback_chain,
                        latency_seconds, request_status, error_message,
                        content_length, stop_reason
                    )
                    VALUES (
                        :conversation_id, :message_id, :agent_id, :agent_name, :account_id, :user_id,
                        :llm_provider, :llm_model,
                        :input_tokens, :output_tokens, :cached_input_tokens, :cache_creation_tokens,
                        :is_cache_hit, :cache_hit_rate,
                        :retry_attempt, :fallback_from_model, :fallback_chain,
                        :latency_seconds, :request_status, :error_message,
                        :content_length, :stop_reason
                    );
                    """)
                connection.execute(
                    insert_query,
                    {
                        "conversation_id": conversation_id,
                        "message_id": message_id,
                        "agent_id": agent_id,
                        "agent_name": agent_name,
                        "account_id": account_id,
                        "user_id": user_id,
                        "llm_provider": token_usage.get("llm_provider"),
                        "llm_model": token_usage.get("llm_model"),
                        "input_tokens": token_usage.get("input_tokens", 0),
                        "output_tokens": token_usage.get("output_tokens", 0),
                        "cached_input_tokens": 0,
                        "cache_creation_tokens": 0,
                        "is_cache_hit": False,
                        "cache_hit_rate": None,
                        "retry_attempt": 0,
                        "fallback_from_model": None,
                        "fallback_chain": None,
                        "latency_seconds": token_usage.get("latency_seconds", 0),
                        "request_status": token_usage.get("request_status", "success"),
                        "error_message": token_usage.get("error_message"),
                        "content_length": token_usage.get("content_length", 0),
                        "stop_reason": token_usage.get("stop_reason", None),
                    },
                )
                logger.info(
                    f"[LLMTokenLogger] Logged token usage for conversation {conversation_id}, "
                    f"message {message_id}: input={token_usage.get('input_tokens', 0)}, "
                    f"output={token_usage.get('output_tokens', 0)}"
                )
    except Exception as e:
        logger.error(f"[LLMTokenLogger] Failed to log token usage: {e}")
