import json
from enum import StrEnum
from typing import Any, Dict, List, Tuple

import config
from db import database
from psycopg2 import extras, sql


class TaskStatus(StrEnum):
    PROCESSING = "Processing"
    COMPLETED = "Completed"
    FAILED = "Failed"
    TIMEOUT = "Timeout"
    TODO = "Todo"


IGNORE_STATUS: List[str] = [
    TaskStatus.PROCESSING.name,
    TaskStatus.COMPLETED.name,
    TaskStatus.FAILED.name,
    TaskStatus.TIMEOUT.name,
]


def get_agent_tasks(tenant_id: str, cloud_account_id: str, agent_id: str) -> Tuple[List[Dict[str, Any]], int]:
    get_agent_task_sql = sql.SQL("""
        select
            id,
            payload,
            source
        from
            agent_task
        where
            status not in %s
            and cloud_account_id = %s
            and tenant = %s
        order by
            created_at asc
         limit %s
    """)
    db_tasks = database.run_query(
        get_agent_task_sql,
        values=[tuple(IGNORE_STATUS), cloud_account_id, tenant_id, config.Configs.TASK_BATCH_LIMIT],
        cursor_factory=extras.RealDictCursor,
    )
    if not db_tasks or len(db_tasks) == 0:
        return [], 0
    tasks: List[Dict[str, Any]] = []
    for row in db_tasks[: config.Configs.TASK_BATCH_LIMIT]:
        task = {"payload": row["payload"], "source": row["source"], "task_id": row["id"]}
        tasks.append(task)
    if len(tasks) > 0:
        processing_task_ids = [value["task_id"] for value in tasks]
        update_query = sql.SQL("update agent_task set status = %s where id in %s")
        database.run_query(update_query, [TaskStatus.PROCESSING.name, tuple(processing_task_ids)])
    rem_tasks_count: int = len(db_tasks) - len(tasks)
    return tasks, rem_tasks_count


def save_task_status(tenant_id: str, cloud_account_id: str, agent_id: str, task_id: str, data=None):
    if data is None:
        data = {}
    status = TaskStatus.COMPLETED.name
    if not data["success"]:
        status = TaskStatus.FAILED.name
    update_query = sql.SQL("update agent_task set status = %s , response = %s where id = %s")
    database.run_query(update_query, values=[status, json.dumps(data), task_id])


def clean_up_task():

    update_query = sql.SQL(
        "update agent_task set status = %s where status in ('TODO') and "
        "created_at < (CURRENT_TIMESTAMP - INTERVAL '%s seconds')"
    )
    database.run_query(update_query, values=[TaskStatus.TIMEOUT.name, config.Configs.TASK_CLEANUP_WINDOW])

    # cleanup up processing task if duration is more than 2X of taskcleanup window
    update_query = sql.SQL(
        "update agent_task set status = %s where status = %s and created_at < (CURRENT_TIMESTAMP - INTERVAL '%s "
        "seconds')"
    )
    database.run_query(
        update_query,
        values=[TaskStatus.TIMEOUT.name, TaskStatus.PROCESSING.name, config.Configs.TASK_CLEANUP_WINDOW * 2],
    )
