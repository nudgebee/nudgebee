import json
import logging
import warnings
from uuid import UUID

import pandas as pd
from pandas.errors import SettingWithCopyWarning
from psycopg2 import sql as psql
from server.recommendation.recommendation import Recommendation
from server.utils.utils import DatabaseEngine, get_trace
from sqlalchemy import text
from datetime import datetime

logger = logging.getLogger(__name__)
warnings.simplefilter(action="ignore", category=SettingWithCopyWarning)


class DBRecommendation(Recommendation):
    def __init__(
        self,
        url: str,
        table: str,
        account_id: str,
        tenant_id: str,
        deployment_name: str,
        namespace: str,
        resource_id: str,
        persist_recommendation: bool,
    ):
        self.table = table
        self.account_id = account_id
        self.tenant_id = tenant_id
        self.resource_id = resource_id
        self.deployment_name = deployment_name
        self.namespace = namespace
        self.persist_recommendation = persist_recommendation
        try:
            self.engine = DatabaseEngine.get_engine()
        except Exception as e:
            msg = f"Failed to create engine : {e}"
            logger.error(msg)
            raise ValueError(msg)

    def generate_recommendation(
        self,
        last_timestamp: datetime | None,
        original_data: pd.DataFrame,
        predictions_data: pd.DataFrame,
        evidences: pd.DataFrame = pd.DataFrame(),
    ) -> dict:
        with get_trace(__name__).start_as_current_span("generate_recommendation"):
            try:
                conn = self.engine.raw_connection()
                cur = conn.cursor()
                original_data.loc[:, "timestamp"] = pd.to_datetime(original_data["timestamp"], unit="s")
                original_data["timestamp"] = original_data["timestamp"].dt.strftime("%Y-%m-%d %H:%M:%S")
                original_data["replicas"] = original_data["replicas"].apply(float).apply(int)
                original_data_json = json.loads(original_data.to_json(orient="records"))

                # clean predictions
                predictions_data.loc[:, "timestamp"] = pd.to_datetime(predictions_data["timestamp"], unit="s")
                predictions_data["timestamp"] = predictions_data["timestamp"].dt.strftime("%Y-%m-%d %H:%M:%S")
                prediction_json = json.loads(predictions_data.to_json(orient="records"))

                # clean evidences
                evidences.loc[:, "timestamp"] = pd.to_datetime(evidences["timestamp"], unit="s")
                evidences["timestamp"] = evidences["timestamp"].dt.strftime("%Y-%m-%dT%H:%M:%SZ")
                evidences["latency"] = evidences["latency"].apply(float)
                evidences["memory"] = evidences["memory"].apply(float)
                evidences["cpu"] = evidences["cpu"].apply(float)
                evidences["rps"] = evidences["rps"].apply(float)

                evidence_json = json.loads(evidences.to_json(orient="records"))
                recommendation_json = {
                    "kind": "Deployment",
                    "metadata": {"name": self.deployment_name, "namespace": self.namespace},
                    "recommendation": {
                        "resource": "replica",
                        "allocated": original_data_json,
                        "evidence": evidence_json,
                        "recommended": prediction_json,
                        "info": "",
                        "recommended_type": "NB_ML",
                        "description": "replica right sizing for workload",
                        "last_timestamp": last_timestamp.strftime("%Y-%m-%dT%H:%M:%SZ") if last_timestamp else None,
                    },
                }
                if self.persist_recommendation:
                    logger.info(f"Persisting recommendation to DB for the deployment :{self.deployment_name}")
                    # Use parameterized query to prevent SQL injection (was f-string interpolation)
                    query = psql.SQL(
                        "INSERT INTO public.{table}(id, created_at, updated_at, tenant_id, cloud_account_id,"
                        " resource_id, recommendation, recommendation_action, note, severity, estimated_savings,"
                        " status, category, rule_name, dismissed_reason, is_dismissed, account_object_id)"
                        " VALUES(gen_random_uuid(), now(), now(), %s, %s,"
                        " %s, %s, 'Modify'::text, '', 'High'::text,"
                        " 0, 'InProgress'::text, 'RightSizing'::text, 'replica_right_sizing'::text, '', false, '') ON"
                        " CONFLICT ON CONSTRAINT recommendation_cloud_account_id_rule_name_resource_id_category_ DO"
                        " UPDATE SET recommendation= EXCLUDED.recommendation, updated_at = EXCLUDED.updated_at;"
                    ).format(table=psql.Identifier(self.table))
                    cur.execute(
                        query, (self.tenant_id, self.account_id, self.resource_id, json.dumps(recommendation_json))
                    )
            except Exception as e:
                msg = f"Failed to store data to recommendation table: {e}"
                logger.error(msg)
                raise ValueError(msg)
            conn.commit()
            cur.close()
            conn.close()
            return recommendation_json

    def get_recommendations(self, recommendation_ids: list[UUID] = []):
        with get_trace(__name__).start_as_current_span("get_recommendations"):
            try:
                # Use parameterized query to prevent SQL injection (was string concatenation)
                base_query = (
                    "select r.id as recomm_id,r.tenant_id, r.cloud_account_id as account_id"
                    ", r.resource_id, cr.resourse_id as qualified_name, cr.name, recommendation "
                    "from recommendation r join cloud_resourses cr "
                    "on r.resource_id = cr.id where r.status ='InProgress' and rule_name='replica_right_sizing' "
                    "and category ='RightSizing' and cr.status = 'Active'"
                )
                params = {}
                if recommendation_ids:
                    placeholders = ", ".join([f":id_{i}" for i in range(len(recommendation_ids))])
                    base_query += f" and r.id in ({placeholders})"
                    for i, rid in enumerate(recommendation_ids):
                        params[f"id_{i}"] = str(rid)
                base_query += (
                    " and r.cloud_account_id in "
                    "(select a.cloud_account_id from agent a where a.status = 'CONNECTED' and type ='k8s')"
                )
                df = pd.read_sql(text(base_query), self.engine, params=params)
                return df
            except Exception as e:
                msg = f"Failed to get recommendations from DB: {e}"
                logger.error(msg)
                raise ValueError(msg)

    def close_recommendation(self, recommendation_id: UUID):
        query = text("update recommendation set status = 'Closed' where id = :recommendation_id")
        with self.engine.connect() as conn:
            conn.execute(query, {"recommendation_id": recommendation_id})
            conn.commit()
            conn.close()
