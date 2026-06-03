import logging
import os
from typing import Optional

from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session, sessionmaker

logger = logging.getLogger(__name__)

# --- Database Setup ---
DB_URL = os.environ.get("APP_DATABASE_URL", "")
db_engine = None
SessionLocal = None

if DB_URL:
    try:
        db_engine = create_engine(
            DB_URL,
            pool_size=5,
            max_overflow=5,
            pool_recycle=1800,
            pool_pre_ping=True,
        )
        SessionLocal = sessionmaker(bind=db_engine, expire_on_commit=False)
    except Exception as e:
        logger.error(f"Warning: Failed to create database engine: {e}")


def get_db() -> Optional[Session]:
    """Get a new database session. Caller must close it."""
    if not SessionLocal:
        return None
    return SessionLocal()


def init_db():
    """Create benchmark tables if they don't exist, then apply migrations."""
    if not db_engine:
        logger.warning("Cannot init DB: engine not configured (APP_DATABASE_URL)")
        return
    from benchmark_server.models.benchmark_run import Base

    Base.metadata.create_all(db_engine)
    logger.info("Benchmark DB tables initialized")

    _apply_migrations()

    # Recover any runs orphaned by previous server crash
    from benchmark_server.utils.run_manager import (
        recover_orphaned_runs,
        start_followup_sweeper,
    )

    recover_orphaned_runs()

    # Recover infra states stuck in deploying/nuking from a previous crash
    _recover_orphaned_infra()

    # Start the background sweeper: periodically reconciles waiting tests
    # with llm-server state and expires abandoned followups.
    start_followup_sweeper()


def _apply_migrations():
    """Ensure all model columns exist in the DB.

    Compares SQLAlchemy model definitions against the actual DB schema and
    adds any missing columns with ALTER TABLE.  This is a lightweight
    alternative to Alembic — sufficient for additive-only changes (new
    columns).  It does NOT handle column renames, type changes, or drops.
    """
    from sqlalchemy import inspect as sa_inspect

    from benchmark_server.models.benchmark_run import Base

    inspector = sa_inspect(db_engine)
    inspector.clear_cache()

    for table_name, table in Base.metadata.tables.items():
        if not inspector.has_table(table_name):
            continue  # create_all already handled it

        existing_cols = {c["name"] for c in inspector.get_columns(table_name)}
        model_cols = {col.name for col in table.columns}
        missing = model_cols - existing_cols
        if missing:
            logger.info(
                "Migration: %s has %d missing columns: %s",
                table_name,
                len(missing),
                ", ".join(sorted(missing)),
            )

        for col in table.columns:
            if col.name in existing_cols:
                continue

            # Build the column type DDL
            col_type = col.type.compile(dialect=db_engine.dialect)
            nullable = "NULL" if col.nullable else "NOT NULL"
            default = ""
            if col.server_default is not None:
                default = f" DEFAULT {col.server_default.arg}"
            elif col.default is not None and col.default.is_scalar:
                val = col.default.arg
                if isinstance(val, bool):
                    default = f" DEFAULT {str(val).lower()}"
                elif isinstance(val, (int, float)):
                    default = f" DEFAULT {val}"
                elif isinstance(val, str):
                    default = f" DEFAULT '{val}'"

            sql = (
                f'ALTER TABLE "{table_name}" '
                f'ADD COLUMN "{col.name}" {col_type} {nullable}{default}'
            )

            try:
                with db_engine.begin() as conn:
                    conn.execute(text(sql))
                logger.info("Migration: added column %s.%s", table_name, col.name)
            except Exception as exc:
                logger.warning(
                    "Migration: failed to add column %s.%s: %s",
                    table_name,
                    col.name,
                    exc,
                )


def _recover_orphaned_infra():
    """Mark any infra stuck in 'deploying'/'nuking' as failed after server restart."""
    if not db_engine:
        return
    try:
        from benchmark_server.models.benchmark_run import BenchmarkInfraState

        session = SessionLocal()
        try:
            stuck = (
                session.query(BenchmarkInfraState)
                .filter(BenchmarkInfraState.status.in_(["deploying", "nuking"]))
                .all()
            )
            for row in stuck:
                prev = row.status
                row.status = "failed" if prev == "deploying" else "nuke_failed"
                row.error = f"Server restarted while {prev}"
                logger.warning(
                    "Recovered orphaned infra state: agent=%s was '%s' → '%s'",
                    row.agent,
                    prev,
                    row.status,
                )
            if stuck:
                session.commit()
                logger.info("Recovered %d orphaned infra state(s)", len(stuck))
        except Exception:
            session.rollback()
            logger.exception("Failed to recover orphaned infra states")
        finally:
            session.close()
    except Exception:
        logger.exception("Error in _recover_orphaned_infra")


def get_user_email(user_id: str) -> Optional[str]:
    """
    Fetches the email address for a given user_id from the 'users' table.
    """
    if not user_id:
        logger.error("get_user_email: user_id is required")
        return None

    if not db_engine:
        logger.error(
            "get_user_email: Database engine not initialized. Check APP_DATABASE_URL."
        )
        return None

    try:
        with db_engine.connect() as connection:
            query = text("SELECT username FROM users WHERE id = :user_id")
            result = connection.execute(query, {"user_id": user_id}).fetchone()

            if result and result[0]:
                return str(result[0])
            else:
                logger.warning(f"No user found with id: {user_id}")
                return None
    except Exception as e:
        logger.exception(f"Failed to fetch email for user_id {user_id}: {e}")
        return None


def search_user_ids_by_email(pattern: str) -> list:
    """Find user IDs whose username/email contains the pattern."""
    if not pattern or not db_engine:
        return []
    try:
        with db_engine.connect() as conn:
            result = conn.execute(
                text("SELECT id FROM users WHERE LOWER(username) LIKE :p"),
                {"p": f"%{pattern.lower()}%"},
            ).fetchall()
            return [str(r[0]) for r in result]
    except Exception:
        return []
