from abc import ABC, abstractmethod

import pandas as pd


class PredictionStore(ABC):
    @abstractmethod
    def store_predictions(self, model_name: str, predictions: pd.DataFrame):
        pass
