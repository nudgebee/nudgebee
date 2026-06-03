import logging

import pandas as pd
from psycopg2.extras import execute_values
from sqlalchemy import create_engine

from server.predictions_store.prediction_store import PredictionStore
from server.utils.utils import get_trace

logger = logging.getLogger(__name__)


class DatabasePredictionStore(PredictionStore):
    def __init__(self, url: str, table: str):
        self.table = table
        try:
            self.engine = create_engine(url)
        except Exception as e:
            msg = f"Failed to create engine : {e}"
            logger.error(msg)
            raise ValueError(msg)

    def store_predictions(self, model_name: str, predictions: pd.DataFrame):
        with get_trace(__name__).start_as_current_span("store_predictions"):
            try:
                conn = self.engine.raw_connection()
                cur = conn.cursor()
                # Batch upsert: single multi-row INSERT instead of N individual statements.
                # execute_values sends one round trip per page vs N individual round trips.
                # page_size=250 keeps each statement under ~2500 values (250 rows × 10 cols),
                # avoiding excessive query-parse memory and tuple overhead in PostgreSQL.
                _PAGE_SIZE = 250
                sql = (
                    f"INSERT INTO {self.table} (id, inference_time, tenant_id, account_id, namespace, deployment,"
                    " model, replicas, cpu, memory) VALUES %s ON CONFLICT ON"
                    f" CONSTRAINT {self.table}_un DO UPDATE SET replicas = EXCLUDED.replicas, cpu = EXCLUDED.cpu,"
                    " memory = EXCLUDED.memory"
                )
                values = [
                    (
                        row["id"],
                        row["inference_time"],
                        row["tenant_id"],
                        row["account_id"],
                        row["namespace"],
                        row["deployment"],
                        row["model"],
                        row["replicas"],
                        row["cpu"],
                        row["memory"],
                    )
                    for _, row in predictions.iterrows()
                ]
                execute_values(cur, sql, values, page_size=_PAGE_SIZE)
            except Exception as e:
                msg = f"Failed to store data to inference table: {e}"
                logger.error(msg)
                raise ValueError(msg)
            conn.commit()
            cur.close()
            conn.close()
