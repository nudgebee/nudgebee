import pandas as pd

from server.metrics.metrics import Metrics
from server.utils.utils import get_trace


class Database(Metrics):
    def __init__(self, db_url: str, username: str, password: str, schema: str, database_name: str, table: str):
        self.db_url = db_url
        self.username = username
        self.password = password
        self.schema = schema
        self.database_name = database_name
        self.table = table

    def get_metrics(self) -> pd.DataFrame:
        with get_trace(__name__).start_as_current_span("get_metrics"):
            return pd.DataFrame()
