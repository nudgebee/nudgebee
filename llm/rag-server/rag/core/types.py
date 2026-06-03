"""Core types replacing langchain_core equivalents.

Provides Document, Embeddings, LLM, and LLMResult used throughout the RAG server.
"""

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional


@dataclass
class Document:
    """A document with page content and metadata."""

    page_content: str
    metadata: Dict[str, Any] = field(default_factory=dict)
    id: Optional[str] = None


class Embeddings(ABC):
    """Abstract base class for embedding providers."""

    @abstractmethod
    def embed_documents(self, texts: List[str]) -> List[List[float]]:
        """Embed a list of texts."""

    @abstractmethod
    def embed_query(self, text: str) -> List[float]:
        """Embed a single query text."""


@dataclass
class LLMResult:
    """Result from an LLM generate call."""

    text: str
    input_tokens: int = 0
    output_tokens: int = 0


class LLM(ABC):
    """Abstract base class for LLM providers used in reranking."""

    model: str = ""

    @abstractmethod
    def generate(self, prompt: str) -> LLMResult:
        """Send a prompt and return the generated text with token usage."""
