"""SQLAlchemy models for benchmark runs and test results."""

from sqlalchemy import (
    Column,
    DateTime,
    Float,
    ForeignKey,
    Integer,
    String,
    Text,
    func,
)
from sqlalchemy.dialects.postgresql import JSONB
from sqlalchemy.orm import DeclarativeBase, relationship


class Base(DeclarativeBase):
    pass


class BenchmarkRun(Base):
    __tablename__ = "llm_benchmark_runs"

    run_id = Column(String(12), primary_key=True)
    agent = Column(String(64), nullable=False, index=True)
    state = Column(String(20), nullable=False, default="running", index=True)
    phase = Column(String(40), nullable=False, default="initializing")

    # Who triggered it
    user_id = Column(String(64), nullable=True)
    account_id = Column(String(64), nullable=True)
    tenant_id = Column(String(64), nullable=True)

    # LLM config
    tool_config = Column(String(64), nullable=True)

    # Timestamps
    created_at = Column(DateTime, server_default=func.now(), nullable=False)
    updated_at = Column(
        DateTime, server_default=func.now(), onupdate=func.now(), nullable=False
    )
    started_at = Column(DateTime, nullable=True)
    completed_at = Column(DateTime, nullable=True)
    duration_seconds = Column(Float, default=0.0)

    # Progress
    progress_current = Column(Integer, default=0)
    progress_total = Column(Integer, default=0)
    current_query = Column(String(200), default="")

    # Run config (stored for rerun)
    max_tests = Column(Integer, nullable=True)
    test_indices = Column(String(200), nullable=True)
    test_filter = Column(String(200), nullable=True)
    tag_filter = Column(String(200), nullable=True)
    parallel_workers = Column(Integer, nullable=True)
    run_name = Column(String(100), nullable=True)
    cc_emails = Column(JSONB, nullable=True)

    # Structured data
    errors = Column(JSONB, default=list)
    report_json = Column(JSONB, nullable=True)

    # Relationship
    test_results = relationship(
        "BenchmarkTestResult",
        back_populates="run",
        cascade="all, delete-orphan",
        order_by="BenchmarkTestResult.test_index",
    )


class BenchmarkTestResult(Base):
    __tablename__ = "llm_benchmark_test_results"

    id = Column(Integer, primary_key=True, autoincrement=True)
    run_id = Column(
        String(12),
        ForeignKey("llm_benchmark_runs.run_id", ondelete="CASCADE"),
        nullable=False,
        index=True,
    )
    test_id = Column(String(128), nullable=False)
    test_index = Column(Integer, nullable=False)
    status = Column(String(20), nullable=False, default="pending")

    # Query & answers
    query = Column(Text, nullable=True)
    expected_answer = Column(Text, nullable=True)
    actual_answer = Column(Text, nullable=True)

    # LLM conversation references (for re-evaluation)
    conversation_id = Column(
        String(128), nullable=True
    )  # DB conversation UUID (planner, tools)
    polling_conversation_id = Column(
        String(128), nullable=True
    )  # LLM session_id (token usage metrics API)

    # Scores
    answer_similarity = Column(Float, default=0.0)
    answer_relevancy = Column(Float, default=0.0)
    planner_relevancy = Column(Float, default=0.0)
    score_reason = Column(
        Text, nullable=True
    )  # LLM judge feedback for answer_relevancy
    execution_trace = Column(Text, nullable=True)  # Full plan + agent/tool call trace

    # Performance
    duration_seconds = Column(Float, default=0.0)
    setup_duration = Column(Float, default=0.0)  # before_test time
    llm_duration = Column(Float, default=0.0)  # LLM call + eval time
    teardown_duration = Column(Float, default=0.0)  # after_test time
    cost = Column(Float, default=0.0)
    total_tokens = Column(Integer, default=0)
    input_tokens = Column(Integer, default=0)
    output_tokens = Column(Integer, default=0)
    cache_read_tokens = Column(Integer, default=0)

    # Tool usage
    tool_calls_total = Column(Integer, default=0)
    tool_calls_successful = Column(Integer, default=0)
    tool_names = Column(JSONB, default=list)

    # Model info (from token metrics) — stored as JSON arrays
    model_names = Column(JSONB, default=list)
    model_providers = Column(JSONB, default=list)

    # Metadata
    tags = Column(JSONB, default=list)
    error_message = Column(Text, nullable=True)
    error_category = Column(String(30), nullable=True)
    followup_request = Column(JSONB, nullable=True)  # followup data when status=waiting
    created_at = Column(DateTime, server_default=func.now(), nullable=False)
    # Auto-bumped by SQLAlchemy on every UPDATE. Used by the abandoned-
    # followup sweeper to decide which waiting tests have gone stale —
    # ``created_at`` wouldn't work because rows are upserted across
    # followup rounds and we need the last-activity stamp, not first-seen.
    updated_at = Column(
        DateTime,
        server_default=func.now(),
        onupdate=func.now(),
        nullable=False,
    )

    # Relationship
    run = relationship("BenchmarkRun", back_populates="test_results")


class BenchmarkInfraState(Base):
    """Tracks infrastructure deployment state per agent.

    Persists across server restarts so the dashboard knows
    whether infra is deployed after a reboot.
    """

    __tablename__ = "llm_benchmark_infra_state"

    agent = Column(String(64), primary_key=True)
    status = Column(String(20), nullable=False, default="unknown")
    started_at = Column(DateTime, nullable=True)
    finished_at = Column(DateTime, nullable=True)
    error = Column(Text, nullable=True)
    output = Column(Text, nullable=True)  # stdout from deploy/nuke script

    # Deploy params (so UI can show what was used and pre-fill for re-deploy)
    test_indices = Column(String(200), nullable=True)
    max_tests = Column(Integer, nullable=True)
    tag_filter = Column(String(200), nullable=True)
    scenarios = Column(JSONB, nullable=True)  # list of scenario IDs deployed

    updated_at = Column(
        DateTime, server_default=func.now(), onupdate=func.now(), nullable=False
    )
