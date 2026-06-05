from urllib.parse import urlparse, unquote
import logging
from typing import List, Sequence, Dict, Any
import json

from config import Configs
import clickhouse_connect
from clickhouse_connect.driver.client import Client
from clickhouse_connect.driver.query import QueryResult
from clickhouse_connect.driver.summary import QuerySummary
from clickhouse_connect import common

DB_POOL = None
logger = logging.getLogger(__name__)


def create_db_connection_pool() -> Client:
    global DB_POOL
    if DB_POOL is None:
        host: str | None = Configs.CLICKHOUSE_HOST
        port = 8123
        username = Configs.CLICKHOUSE_USER
        password = Configs.CLICKHOUSE_PASSWORD

        # get port from host
        if host is not None and "//" in host:
            parsed_url = urlparse(host)
            if parsed_url.port:
                port = parsed_url.port
            host = parsed_url.hostname
            if parsed_url.username:
                username = unquote(parsed_url.username)
            if parsed_url.password:
                password = unquote(parsed_url.password)

        if host is not None and ":" in host:
            hostport = host.split(":")
            host = hostport[0]
            port = int(hostport[1])

        if host is None:
            host = "localhost"

        common.set_setting("autogenerate_session_id", False)
        DB_POOL = clickhouse_connect.get_client(
            host=str(host),
            port=port,
            username=username,
            password=password,
            database=Configs.CLICKHOUSE_DATABASE,
        )
    return DB_POOL


def select_data(table_name: str, columns: List[str] = [], conditions: Dict[str, Any] = {}) -> QueryResult:
    # SECURITY: table_name, column entries, and condition keys are inlined
    # into the SQL string — ClickHouse has no parameter binding for
    # identifiers. Mirror the Postgres sister helper (db/database.py): pass
    # static literals only; do NOT pass user-supplied strings without an
    # explicit allowlist. Condition values are parameter-bound and safe.
    if Configs.CLICKHOUSE_ENABLED is False:
        return QueryResult()
    conn = None
    try:
        conn = create_db_connection_pool()
        if not columns:
            columns = ["*"]
        column_str = ", ".join(columns)

        query = f"SELECT {column_str} FROM {table_name}"
        result = None
        if conditions:
            condition_str = " AND ".join([f"{k}=%s" for k in conditions.keys()])
            query += f" WHERE {condition_str}"
            values = tuple(conditions.values())
            result = conn.query(query, values)
        else:
            result = conn.query(query)

        return result
    except Exception as e:
        logger.exception(f"Error selecting data from {table_name} table: {e}")
        raise


def run_query(
    query: str,
    values: List[Any] = [],
) -> str | int | Sequence[str] | QuerySummary:
    if Configs.CLICKHOUSE_ENABLED is False:
        return []

    conn = None
    try:
        conn = create_db_connection_pool()
        results = conn.command(query, values)
        logger.debug(f"Executed query: {query}")
        return results
    except Exception as e:
        logger.exception(f"Error executing query: {query}, Error: {e}")
        raise e


def escape_string(string: str) -> str:
    return string.replace("'", "''")


def dict_to_insert_query(table_name: str, data: List[Dict[str, Any]], on_conflict: str = "") -> str:
    # SECURITY: table_name and column names (data[*].keys()) are inlined into
    # the SQL string. Values are inline-escaped via escape_string() below,
    # which is unsafe for untrusted input. ClickHouse has no parameter
    # binding for identifiers, so callers must pass static literals only and
    # restrict use to internally-generated data.
    columns = list(data[0].keys())
    if on_conflict is None:
        on_conflict = ""

    # Build the SQL insert query
    query = f"INSERT INTO {table_name} ({', '.join(columns)}) VALUES "

    # Append the values for each dictionary
    for d in data:
        values = []
        for c in columns:
            value = d[c]
            if value is None:
                values.append("NULL")
            elif isinstance(value, str):
                values.append(f"'{escape_string(value)}'")
            elif isinstance(value, dict):
                values.append(f"'{json.dumps(value)}'")
            else:
                values.append(f"'{str(value)}'")
        query += f"({', '.join(values)}), "

    # Remove the trailing comma and space
    query = query[:-2]
    final_query = f"{query} {on_conflict}"
    return final_query


def insert_data(table_name: str, data: List[Dict[str, Any]], batch_size: int = 1000) -> None:
    if Configs.CLICKHOUSE_ENABLED is False:
        return
    conn = None
    try:
        conn = create_db_connection_pool()
        # Split the data into batches
        batches = [data[i : i + batch_size] for i in range(0, len(data), batch_size)]

        # Insert each batch of data
        for batch in batches:
            # Execute the query
            conn.insert(table_name, data=[list(v.values()) for v in batch], column_names=list(batch[0].keys()))

        logger.debug(f"Inserted data into {table_name} table")

    except Exception as e:
        logger.exception(f"Error inserting data into {table_name} table: {e}")
        raise
