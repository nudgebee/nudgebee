import base64
import json
import logging
from datetime import datetime
from typing import Any, Dict, List
from uuid import UUID, uuid4

from controllers.base import BaseController
from db import database
from psycopg2 import extras
from pydantic import BaseModel, Field, field_validator

logger = logging.getLogger(__name__)


class RunbookOutputRequestAModel(BaseModel):
    success: bool = False
    output: str | None = None
    logs: str | None = None
    error_codes: int | None = None
    error: str | None = None
    task_id: UUID
    pod_config: str | None = None
    warning_code: int | None = None
    warning: str | None = None


class Evidence(BaseModel):
    data: str = ""
    type: str = "json"


class Findings(BaseModel):
    id: UUID = Field(default_factory=uuid4)
    title: str = "NudgeBee notification"
    description: str | None = None
    source: str | None = None
    aggregation_key: str = "Generic finding key"
    failure: bool = True
    finding_type: str = "issue"
    category: str | None = None
    priority: str = "INFO"
    subject_type: str | None = None
    subject_name: str | None = None
    subject_namespace: str | None = None
    subject_node: str | None = None
    service_key: str = "None/None/None"
    cluster: str = ""
    account_id: str = ""
    video_links: List[str] = []
    starts_at: datetime = Field(default_factory=datetime.utcnow)
    updated_at: datetime = Field(default_factory=datetime.utcnow)
    fingerprint: str = ""
    evidence: List[Evidence] = [Evidence()]


class RunbookSidecarRequest(BaseModel):
    success: bool = True
    findings: List[Findings] = []


class AgentTaskPayload(BaseModel):
    sinks: str | None
    origin: str
    timestamp: int
    action_name: str
    action_params: Dict[str, Any]

    def __init__(self, **data: Any):
        data["timestamp"] = int(data["timestamp"]) if isinstance(data["timestamp"], float) else data["timestamp"]
        super().__init__(**data)


class AgentTask(BaseModel):
    id: UUID = Field(default_factory=uuid4)
    created_at: datetime = Field(default_factory=datetime.utcnow)
    updated_at: datetime = Field(default_factory=datetime.utcnow)
    cloud_account_id: UUID
    tenant: UUID
    action: str
    payload: AgentTaskPayload
    status: str
    source: str = "autoplaybook"
    source_id: UUID
    response: Dict[str, Any] | None = None

    @classmethod
    def get_from_db(cls, id: UUID):
        # Assume there is a method in your database module to fetch data by ID
        db_data = database.select_data(
            table_name="agent_task",
            cursor_factory=extras.RealDictCursor,
            conditions={"id": id},
        )

        if db_data:
            # Initialize the BaseModel with the fetched data
            return cls(**dict(db_data[0]))
        else:
            raise Exception(f"Not able to find Task with id {id}")

    def save_to_db(self):
        model_sv = json.loads(self.model_dump_json())
        database.insert_data(
            table_name="agent_task",
            data=[model_sv],
            on_conflict="ON CONFLICT(id) DO UPDATE SET status = (EXCLUDED.status), response = (EXCLUDED.response)",
        )


class RunbookTaskOutputDBModel(BaseModel):
    id: UUID = Field(default_factory=uuid4)
    runbook_id: UUID
    task_id: UUID
    output: str
    tenant_id: UUID
    account_id: UUID

    @field_validator("output")
    @classmethod
    def validate_output(cls, value):
        try:
            base64.b64decode(value)
        except Exception:
            raise ValueError("Invalid output format")
        return value

    def save_to_db(self):
        model_sv = json.loads(self.model_dump_json())
        database.insert_data(
            table_name="runbook_task_output",
            data=[model_sv],
            on_conflict=("ON CONFLICT(id) DO UPDATE SET " "output = (EXCLUDED.output)"),
        )


class AutoPilotController(BaseController):
    def get_task_details(self, task_id: UUID):
        result = database.select_data(
            table_name="auto_playbook_task", conditions={"id": str(task_id)}, cursor_factory=extras.RealDictCursor
        )
        if result:
            return result[0]
        raise ValueError(f"Task id {task_id} not found")

    def post_async(self, account_id: UUID, tenant_id: UUID, request_data: Dict[str, Any]) -> None:
        request = RunbookOutputRequestAModel(**request_data)
        logger.info(
            f"got runbook output data for task {request.task_id} with success {request.success}",
        )
        task_details = self.get_task_details(task_id=request.task_id)
        runbook_id = task_details.get("auto_playbook_id")
        action_id = task_details.get("action_id")
        if request.output:
            task_output_opj = RunbookTaskOutputDBModel(
                runbook_id=runbook_id,
                task_id=request.task_id,
                tenant_id=tenant_id,
                account_id=account_id,
                output=request.output,
            )
            task_output_opj.save_to_db()
        agent_task = AgentTask.get_from_db(action_id)
        agent_data = request.model_dump()
        agent_data.pop("output", None)
        data = {"data": agent_data, "type": "json"}
        evidence = Evidence(data=json.dumps([data], default=str))
        findings = Findings(evidence=[evidence])
        agent_resp = RunbookSidecarRequest(findings=[findings])
        if request.success:
            agent_task.status = "COMPLETED"
            agent_resp.success = True
        else:
            agent_task.status = "FAILED"
            agent_resp.success = True
        agent_task.response = agent_resp.model_dump()
        agent_task.save_to_db()
