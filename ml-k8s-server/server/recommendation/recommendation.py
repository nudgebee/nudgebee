from abc import ABC, abstractmethod
from datetime import datetime
from uuid import UUID

import pandas as pd


class Recommendation(ABC):
    @abstractmethod
    def generate_recommendation(
        self,
        last_timestamp: datetime | None,
        original_data: pd.DataFrame,
        predictions_data: pd.DataFrame,
        evidences: pd.DataFrame = pd.DataFrame(),
    ):
        pass

    @abstractmethod
    def get_recommendations(self, recommendation_ids: list[UUID] = []):
        pass
