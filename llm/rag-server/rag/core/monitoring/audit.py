import json
import logging
import threading

from rag.core.types import Document
from sqlalchemy import create_engine
from sqlalchemy import text
from sqlalchemy.pool import QueuePool

from utils.config import DBConfig

# Create a thread-safe connection pool
engine = create_engine(DBConfig.url, poolclass=QueuePool, pool_size=5, max_overflow=20, pool_pre_ping=True)


def save_audit_async(cloud_account_id, module, query, conversation_id, documents: list[tuple[Document, float]]):
    # async call to save_audit
    threading.Thread(target=save_audit, args=(cloud_account_id, module, query, conversation_id, documents)).start()


def save_audit(cloud_account_id, module, request, conversation_id, documents: list[tuple[Document, float]]):
    try:
        with engine.connect() as connection:
            with connection.begin() as transaction:
                score = 0.0
                if len(documents) > 0:
                    score = documents[0][1]

                response = []
                for doc, s in documents:
                    response.append({"page_content": doc.page_content, "score": s})

                query = text(
                    "INSERT INTO llm_rag_audit (cloud_account_id, module, query, score, response, conversation_id) "
                    "VALUES (:cloud_account_id, :module, :query, :score, :response, :conversation_id)"
                )
                connection.execute(
                    query,
                    {
                        "cloud_account_id": cloud_account_id,
                        "module": module,
                        "query": request,
                        "score": score,
                        "response": json.dumps(response),
                        "conversation_id": conversation_id if conversation_id else None,
                    },
                )
                transaction.commit()
    except Exception as e:
        logging.exception("Error saving audit: %s", e)
