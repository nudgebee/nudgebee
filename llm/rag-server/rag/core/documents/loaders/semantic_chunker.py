"""Semantic text chunker replacing langchain_experimental.text_splitter.SemanticChunker.

Splits text into semantically coherent chunks using embedding similarity
to detect breakpoints between sentences.
"""

import logging
import re
from typing import List

import numpy as np

from rag.core.types import Document

logger = logging.getLogger(__name__)

# Cosine similarity threshold — sentences below this are split into separate chunks
_DEFAULT_THRESHOLD_PERCENTILE = 90


def _cosine_similarity(a: np.ndarray, b: np.ndarray) -> float:
    dot = np.dot(a, b)
    norm = np.linalg.norm(a) * np.linalg.norm(b)
    return float(dot / norm) if norm > 0 else 0.0


def _split_sentences(text: str) -> List[str]:
    """Split text into sentences."""
    sentences = re.split(r"(?<=[.!?])\s+", text.strip())
    return [s.strip() for s in sentences if s.strip()]


class SemanticChunker:
    """Split text into chunks based on embedding similarity between sentences."""

    def __init__(self, embeddings, threshold_percentile: int = _DEFAULT_THRESHOLD_PERCENTILE):
        self.embeddings = embeddings
        self.threshold_percentile = threshold_percentile

    def create_documents(self, texts: List[str]) -> List[Document]:
        combined = "\n".join(texts)
        sentences = _split_sentences(combined)

        if len(sentences) <= 1:
            return [Document(page_content=combined)] if combined.strip() else []

        # Embed all sentences
        vectors = self.embeddings.embed_documents(sentences)
        arr = np.array(vectors)

        # Compute similarity between consecutive sentences
        similarities = []
        for i in range(len(arr) - 1):
            sim = _cosine_similarity(arr[i], arr[i + 1])
            similarities.append(sim)

        if not similarities:
            return [Document(page_content=combined)]

        # Determine threshold: split where similarity drops below percentile
        threshold = float(np.percentile(similarities, 100 - self.threshold_percentile))

        # Build chunks
        chunks: List[List[str]] = [[sentences[0]]]
        for i, sim in enumerate(similarities):
            if sim < threshold:
                chunks.append([sentences[i + 1]])
            else:
                chunks[-1].append(sentences[i + 1])

        return [Document(page_content=" ".join(chunk)) for chunk in chunks if any(s.strip() for s in chunk)]
