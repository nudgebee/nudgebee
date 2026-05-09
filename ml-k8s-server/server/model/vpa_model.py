import pandas as pd
from server.model.models import Models
from server.utils.utils import get_trace


class VPAModel(Models):
    def __init__(self, model_type):
        self.model_type = model_type

    def get_predictions(self, data: pd.DataFrame) -> pd.DataFrame:
        with get_trace(__name__).start_as_current_span("get_predictions"):
            return pd.DataFrame()
