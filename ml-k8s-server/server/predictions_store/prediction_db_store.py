import logging

import pandas as pd
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
                for _, row in predictions.iterrows():
                    cur.execute(
                        f"INSERT INTO {self.table} (id, inference_time, tenant_id, account_id, namespace, deployment,"
                        " model,replicas, cpu, memory) VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, %s) ON CONFLICT ON"
                        f" CONSTRAINT {self.table}_un DO UPDATE SET replicas = EXCLUDED.replicas, cpu = EXCLUDED.cpu,"
                        " memory =EXCLUDED.memory;",
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
                        ),
                    )
            except Exception as e:
                msg = f"Failed to store data to inference table: {e}"
                logger.error(msg)
                raise ValueError(msg)
            conn.commit()
            cur.close()
            conn.close()
