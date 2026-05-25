import logging
from typing import List
from uuid import UUID

from flask import Blueprint, jsonify, request
from server.message import SyncRecommendationData
from server.recommendation.recommendation_db import DBRecommendation
from server.utils.utils import DBConfig, RecommendationConfig, get_trace

logger = logging.getLogger(__name__)
app = Blueprint("prediction", __name__)


@app.route("/update_recommendations", methods=["POST"])
def update_predictions():
    content = request.get_json()
    recommendation_ids: List[UUID] = list(map(UUID, content.get("recommendation_id", [])))
    with get_trace(__name__).start_as_current_span("update_recommendations"):
        db_recommendation = DBRecommendation(
            url=DBConfig.url,
            table=RecommendationConfig.table_name,
            account_id="",
            tenant_id="",
            resource_id="",
            deployment_name="",
            namespace="",
            persist_recommendation=False,
        )
        recommendations = db_recommendation.get_recommendations(recommendation_ids=recommendation_ids)
        logger.info("Updating recommendations ----- ")
        for row in recommendations.itertuples(index=True, name="Pandas"):
            recomm_id = getattr(row, "recomm_id")
            SyncRecommendationData(recommendation_id=recomm_id).publish()

    return jsonify({"message": "Recommendations updated successfully"})
