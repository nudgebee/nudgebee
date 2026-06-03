"""File-based document loaders replacing langchain_community.document_loaders.

Provides JSONLoader, CSVLoader, TextLoader, UnstructuredXMLLoader, and
SQLDatabaseLoader using stdlib + existing dependencies (jq, bs4, sqlalchemy).
"""

import csv
import json
import logging
from typing import Any, Callable, Dict, List, Optional

import jq as jq_lib  # type: ignore[import-not-found]
from sqlalchemy import RowMapping, text

from rag.core.types import Document

logger = logging.getLogger(__name__)


class JSONLoader:
    """Load documents from a JSON file using a jq schema."""

    def __init__(self, file_path: str, jq_schema: str = ".[]", text_content: bool = True):
        self.file_path = file_path
        self.jq_schema = jq_schema
        self.text_content = text_content

    def load(self) -> List[Document]:
        with open(self.file_path, "r", encoding="utf-8") as f:
            data = json.load(f)

        compiled = jq_lib.compile(self.jq_schema)
        items = compiled.input_value(data).all()

        documents = []
        for item in items:
            if self.text_content:
                page_content = str(item) if not isinstance(item, str) else item
            else:
                page_content = json.dumps(item) if not isinstance(item, str) else item
            documents.append(Document(page_content=page_content, metadata={"source": self.file_path}))
        return documents


class CSVLoader:
    """Load documents from a CSV file."""

    def __init__(self, file_path: str, csv_args: Optional[Dict[str, Any]] = None):
        self.file_path = file_path
        self.csv_args = csv_args or {}

    def load(self) -> List[Document]:
        documents = []
        with open(self.file_path, "r", encoding="utf-8") as f:
            reader = csv.DictReader(f, **self.csv_args)
            for i, row in enumerate(reader):
                page_content = "\n".join(f"{k}: {v}" for k, v in row.items() if v)
                documents.append(Document(page_content=page_content, metadata={"source": self.file_path, "row": i}))
        return documents


class TextLoader:
    """Load a text file as a single document."""

    def __init__(self, file_path: str):
        self.file_path = file_path

    def load(self) -> List[Document]:
        with open(self.file_path, "r", encoding="utf-8") as f:
            content = f.read()
        return [Document(page_content=content, metadata={"source": self.file_path})]


class UnstructuredXMLLoader:
    """Load an XML file using unstructured library."""

    def __init__(self, file_path: str):
        self.file_path = file_path

    def load(self) -> List[Document]:
        from unstructured.partition.xml import partition_xml

        elements = partition_xml(filename=self.file_path)
        text = "\n".join(str(el) for el in elements)
        return [Document(page_content=text, metadata={"source": self.file_path})]


class SQLDatabaseLoader:
    """Load documents from a SQL query."""

    def __init__(
        self,
        query: str,
        db: Any,
        page_content_mapper: Optional[Callable[[RowMapping, Optional[List[str]]], str]] = None,
        params: Optional[Dict[str, Any]] = None,
    ):
        self.query = query
        self.db = db
        self.page_content_mapper = page_content_mapper or self._default_mapper
        self.params = params or {}

    @staticmethod
    def _default_mapper(row: RowMapping, column_names: Optional[List[str]] = None) -> str:
        if column_names is None:
            column_names = list(row.keys())
        return "\n".join(f"{col}: {row[col]}" for col in column_names if col in row)

    def load(self) -> List[Document]:
        documents = []
        engine = self.db.engine
        with engine.connect() as conn:
            # Use parameterized queries to prevent SQL injection
            result = conn.execute(text(self.query), self.params)
            for row in result.mappings():
                page_content = self.page_content_mapper(row, None)
                documents.append(Document(page_content=page_content, metadata=dict(row)))
        return documents


class SQLDatabase:
    """Thin wrapper around a SQLAlchemy engine for compatibility."""

    def __init__(self, engine: Any, include_tables: Optional[List[str]] = None, view_support: bool = False):
        self.engine = engine
        self.include_tables = include_tables
        self.view_support = view_support
