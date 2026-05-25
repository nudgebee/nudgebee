from abc import ABC, abstractmethod

import pandas as pd


class Metrics(ABC):
    @abstractmethod
    def get_metrics(self) -> pd.DataFrame:
        pass
