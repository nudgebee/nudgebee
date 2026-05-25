import json
import threading
from urllib.parse import urlparse, unquote, parse_qs
import psycopg2.pool
import logging
from typing import List, Union, Optional, Dict, Any, Tuple
from psycopg2 import sql
from config import Configs

DB_POOL = None
logger = logging.getLogger(__name__)
DB_POOL_LOCK = threading.Lock()
_PUTCONN_FAIL_MSG = "Failed to return connection to pool, closing it directly"


def sanitize_string_for_postgres(value):
    """
    Sanitize string values to remove null bytes that cause PostgreSQL errors.
    This handles both actual null bytes (\x00) and unicode null byte escape sequences (\\u0000).

    :param value: The value to sanitize
    :return: Sanitized value with null bytes removed
    """
    if isinstance(value, str):
        # Remove actual null bytes
        sanitized = value.replace("\x00", "")
        # Remove unicode null byte escape sequences that would be converted to null bytes by PostgreSQL JSON parser
        sanitized = sanitized.replace("\\u0000", "")
        return sanitized
    return value


def create_db_connection_pool() -> psycopg2.pool.AbstractConnectionPool:
    global DB_POOL
    if DB_POOL is None:
        with DB_POOL_LOCK:
            # Double-check locking to prevent race condition
            if DB_POOL is None:
                parsed_url = urlparse(Configs.COLLECTOR_DB_URL)
                query_params = parse_qs(parsed_url.query)
                sslmode = query_params.get("sslmode", ["require"])[0]
                DB_POOL = psycopg2.pool.ThreadedConnectionPool(
                    minconn=2,
                    maxconn=20,
                    database=parsed_url.path[1:],
                    user=unquote(parsed_url.username) if parsed_url.username else None,
                    password=unquote(parsed_url.password) if parsed_url.password else None,
                    host=parsed_url.hostname,
                    port=parsed_url.port,
                    sslmode=sslmode,
                )
    return DB_POOL


def select_data(
    table_name: str, columns: List[str] = [], conditions: Dict[str, Any] = {}, cursor_factory=None
) -> List[Any]:
    conn = None
    try:
        conn = create_db_connection_pool().getconn()
        with conn.cursor(cursor_factory=cursor_factory) as cur:
            if not columns:
                columns = ["*"]
            column_str = ", ".join(columns)

            query = f"SELECT {column_str} FROM {table_name}"
            if conditions:
                condition_str = " AND ".join([f"{k}=%s" for k in conditions.keys()])
                query += f" WHERE {condition_str}"
                values = tuple(conditions.values())
                cur.execute(query, values)
            else:
                cur.execute(query)

            rows: List[Any] = cur.fetchall()

            logger.debug(f"Selected data from {table_name} table")

            return rows
    except Exception as e:
        logger.exception(f"Error selecting data from {table_name} table: {e}")
        raise
    finally:
        if conn:
            try:
                create_db_connection_pool().putconn(conn)
            except Exception:
                logger.exception(_PUTCONN_FAIL_MSG)
                try:
                    conn.close()
                except Exception:
                    pass


def run_query(
    query: Union[str, sql.SQL, sql.Composable],
    values: Optional[List[Any]] = None,
    cursor_factory=None,
) -> List[Any]:
    conn = None
    try:
        conn = create_db_connection_pool().getconn()
        results = []
        with conn.cursor(cursor_factory=cursor_factory) as cur:
            if values:
                cur.execute(query, values)
            else:
                cur.execute(query)
            try:
                if cur.rowcount > 0:
                    results = cur.fetchall()
            except psycopg2.ProgrammingError:
                logger.debug(f"No Resultset, likely update/insert, rowcount: {cur.rowcount}, query: {query}")
                results = []
            conn.commit()
        logger.debug(f"Executed query: {query}")
        return results
    except Exception as e:
        logger.exception(f"Error executing query: {query}, Error: {e}")
        raise e
    finally:
        if conn:
            try:
                create_db_connection_pool().putconn(conn)
            except Exception:
                logger.exception(_PUTCONN_FAIL_MSG)
                try:
                    conn.close()
                except Exception:
                    pass


def escape_string(string: str) -> str:
    return string.replace("'", "''")


def dict_to_insert_query(table_name: str, data: List[Dict[str, Any]], on_conflict="") -> Tuple[str, List[Any]]:
    """
    Generates a parameterized SQL INSERT query from a list of dictionaries.

    :param table_name: Name of the table.
    :param data: List of dictionaries, where keys are column names and values are the data to insert.
    :param on_conflict: The ON CONFLICT clause for handling duplicates (optional).
    :return: A tuple containing the query string and the parameters as a list of tuples.
    """
    if not data:
        raise ValueError("Data list cannot be empty.")

    columns = list(data[0].keys())

    # Build the SQL insert query template with placeholders for parameterized query
    query = f"INSERT INTO {table_name} ({', '.join(columns)}) VALUES "

    # Create placeholders for each value set (e.g., (%s, %s, %s))
    value_placeholders = "(" + ", ".join(["%s"] * len(columns)) + ")"
    query += ", ".join([value_placeholders] * len(data))

    # Add the ON CONFLICT clause if provided
    if on_conflict:
        query += f" {on_conflict}"

    # Gather the values for parameterization
    parameters = []
    for d in data:
        values = []
        for col in columns:
            value = d.get(col)
            if isinstance(value, dict) or isinstance(value, list):
                # Convert dict to JSON string and sanitize
                json_str = json.dumps(value)
                json_str = sanitize_string_for_postgres(json_str)
                values.append(json_str)
            else:
                # Sanitize string values to remove null bytes
                sanitized_value = sanitize_string_for_postgres(value)
                values.append(sanitized_value)
        parameters.extend(values)

    return query, parameters


def insert_data(table_name: str, data: List[Dict[str, Any]], batch_size: int = 1000, on_conflict: str = "") -> None:
    conn = None
    try:
        conn = create_db_connection_pool().getconn()
        with conn.cursor() as cur:
            # Split the data into batches
            batches = [data[i : i + batch_size] for i in range(0, len(data), batch_size)]

            # Insert each batch of data
            for batch in batches:
                # Build the SQL insert query
                query, parameters = dict_to_insert_query(table_name, batch, on_conflict)

                # Execute the query
                cur.execute(query, parameters)

            conn.commit()

        logger.debug(f"Inserted data into {table_name} table")

    except Exception as e:
        logger.exception(f"Error inserting data into {table_name} table: {e}")
        if conn:
            conn.rollback()
        raise
    finally:
        if conn:
            try:
                create_db_connection_pool().putconn(conn)
            except Exception:
                logger.exception(_PUTCONN_FAIL_MSG)
                try:
                    conn.close()
                except Exception:
                    pass
