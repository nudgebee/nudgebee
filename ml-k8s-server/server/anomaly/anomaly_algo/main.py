import json
import logging
from datetime import timedelta
from uuid import UUID

import pandas as pd

from server.anomaly.anomaly_algo.db_scan import DBSCANConfig, DBScan
from server.anomaly.anomaly_algo.isolation_tree import IsolationTree, IsolationTreeConfig

logger = logging.getLogger(__name__)


isolation_forest_config = {
    "contamination": 0.01,
    "estimators": 400,
    "iqr_multiplier": 0.5,
    "minimum_score_strength": 0.02,
    "threshold": None,
    "random_state": 42,
    "account_id": UUID("a2a30b02-0f67-42e5-a2ab-c658230fd798"),
    "namespace": "nudgebee",
    "deployment": "auto-pilot",
    "anomaly_type": "memory",
    "step": "1m",
    "enabled_smoothing: bool": False,
    "trigger_threshold_max": None,
    "evaluation_period": timedelta(minutes=180),
}
config = IsolationTreeConfig(**isolation_forest_config)

db_scan_config = {"eps": 0.5, "min_samples": 5}
config = DBSCANConfig(**isolation_forest_config)


# Path to your JSON file
file_path = "/Users/mipladmin/nudgebee/nudgebee/ml-k8s-server/server/anomaly/evaluation_data_set.json"

# Read and parse the JSON file
with open(file_path, "r") as f:
    raw_data = json.load(f)

# Extract the data dictionary
data_dict = raw_data[0]["data"]

# Convert to pandas Series with datetime index
ts_series = pd.Series(data_dict)
ts_series.index = pd.to_datetime(ts_series.index.astype(int), unit="ms")
ts_series.name = "memory_usage"

# Display the parsed series
logger.debug(f"Parsed time series:\n{ts_series}")


isolation_algo = IsolationTree(config=config)
algo = DBScan(config=config)
resp = algo.process_metrics(config=config, data=ts_series)  # Example data
logger.debug(f"Response: {resp.to_json()}")
