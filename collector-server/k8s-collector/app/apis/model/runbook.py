from dataclasses import dataclass, field
from typing import List, Optional


@dataclass
class ExecuteRunbookActionSubmitResponse:
    status: str = ""
    error: str = ""
    created_tasks: List[str] = field(default_factory=list)


@dataclass
class LLMActionResponse:
    response: List[str] = field(default_factory=list)
    chain_name: str = ""
    conversation_id: Optional[str] = None
    message_id: Optional[str] = None
    error: str = ""
    session_id: Optional[str] = None
