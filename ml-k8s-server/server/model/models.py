from abc import ABC, abstractmethod

import pandas as pd


class Models(ABC):
    @abstractmethod
    def get_predictions(self, data: pd.DataFrame):
        pass
