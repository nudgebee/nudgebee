"""Nubi-specific LLM server interface.

Provides get_llm_ans() which calls the LLM without an @agent prefix,
unlike agent benchmarks which use call_llm("@agent query").

For shared metrics (get_token_metrics, get_planner_response, get_tool_names),
use llm.agents.common.metrics instead.
"""

import logging

from dotenv import load_dotenv
from benchmark_server.utils.llm_client import (
    call_llm as _call_llm,
    extract_response_text,
    extract_conversation_id,
)

load_dotenv()

logger = logging.getLogger(__name__)


def get_llm_ans(query, account_id, tenant_id, user_id):
    """Send a query to the Nubi LLM API (without @agent prefix).

    Returns dict with 'response', 'conversation_id', and 'convo_id'.
    On failure returns response="SYSTEM_FAILURE".
    """
    default_return = {"response": "SYSTEM_FAILURE", "conversation_id": None}

    logger.info("Sending request - %s...", query[:100])

    try:
        response_data = _call_llm(query, account_id, tenant_id, user_id)

        if not response_data:
            logger.warning("No response from LLM")
            return default_return

        llm_response = extract_response_text(response_data)
        if llm_response:
            conversation_id = response_data.get("data", {}).get("session_id")
            convo_id = extract_conversation_id(response_data)
            logger.info(
                "Successfully received LLM response (length: %d) - "
                "conversation_id: %s, convo_id: %s",
                len(str(llm_response)),
                conversation_id,
                convo_id,
            )
            return {
                "response": response_data["data"].get("response", llm_response),
                "conversation_id": conversation_id,
                "convo_id": convo_id,
            }
        else:
            logger.warning("Unexpected response format: %s", response_data)

    except Exception as e:
        logger.error("LLM request failed: %s", e)

    logger.error("Returning SYSTEM_FAILURE")
    return default_return
