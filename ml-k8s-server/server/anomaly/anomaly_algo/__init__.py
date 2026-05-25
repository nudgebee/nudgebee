def get_anomaly_algo(algo_nm: str):
    """
    Factory function to get the anomaly detection algorithm class based on the algorithm name.

    :param algo_nm: Name of the anomaly detection algorithm.
    :return: Anomaly detection algorithm class.
    """
    from server.anomaly.anomaly_algo.isolation_tree import IsolationTree, IsolationTreeConfig
    from server.anomaly.anomaly_algo.db_scan import DBScan, DBSCANConfig
    from server.anomaly.anomaly_algo.zscore import ZScore, ZScoreConfig

    algo_map = {
        "ISOLATION_TREE": (IsolationTree, IsolationTreeConfig),
        "DB_SCAN": (DBScan, DBSCANConfig),
        "ZSCORE": (ZScore, ZScoreConfig),
    }

    if algo_nm not in algo_map:
        raise ValueError(f"Unsupported anomaly detection algorithm: {algo_nm}")

    return algo_map[algo_nm]
