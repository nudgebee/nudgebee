# Anomaly Detection System

This module provides comprehensive anomaly detection capabilities for Kubernetes monitoring metrics using machine learning techniques.

## Overview

The anomaly detection system uses IsolationForest algorithm combined with statistical analysis to identify unusual patterns in time series metrics. It's designed to minimize false positives while maintaining high sensitivity for genuine anomalies.

## Supported Metrics

### Memory Usage
- **Query**: Container memory working set bytes
- **Threshold**: 50 MB minimum
- **Algorithm**: IsolationForest with dual-criteria filtering
- **Parameters**:
  - Contamination: 0.01 (1% outliers)
  - IQR Multiplier: 2.5
  - Minimum Score Strength: 0.15
  - Change Threshold: 30% (reduced from 50%)
  - Elevation Factor: 1.4x (reduced from 1.8x)

### CPU Usage
- **Query**: CPU usage percentage
- **Threshold**: 5% minimum
- **Algorithm**: IsolationForest with statistical thresholds
- **Parameters**:
  - Contamination: 0.015 (1.5% outliers)
  - IQR Multiplier: 2.0
  - Minimum Score Strength: 0.12
  - Change Threshold: 50% (reduced from 75%)

### Error Rate
- **Query**: Error rate percentage
- **Threshold**: 0.1% minimum
- **Algorithm**: IsolationForest with constant data detection
- **Parameters**:
  - Contamination: 0.01
  - IQR Multiplier: 2.0
  - Minimum Score Strength: 0.15

## Key Features

### False Positive Reduction
- **Constant Data Detection**: Filters out metrics with no variation (e.g., all zeros)
- **Dual-Criteria Filtering**: Requires both anomaly score and significant change
- **Conservative Parameters**: Low contamination rates for precision
- **Evaluation Period Filtering**: Analyzes only the specified evaluation window

### Time-Based Analysis
- **Training Period**: Uses historical data for model training
- **Evaluation Period**: Focuses anomaly detection on recent time window
- **Timezone Handling**: Robust timezone compatibility for timestamp comparisons

### Memory-Specific Enhancements
- **Elevation Factor**: Requires 40% elevation above recent baseline (reduced from 80%)
- **Change Threshold**: Requires 30% change for anomaly consideration (reduced from 50%)
- **Rolling Baseline**: Uses 30-point rolling median for comparison

## Usage

### Basic Example
```python
from server.anomaly.anomaly import detect_anomaly
import pandas as pd
import datetime

response = detect_anomaly(
    account_id="your-account-id",
    namespace="your-namespace", 
    deployment="your-deployment",
    anomaly_type="memory",
    start_time=datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(hours=24),
    end_time=datetime.datetime.now(datetime.timezone.utc),
    evaluation_period=pd.Timedelta(hours=1)
)

print(f"Anomalies detected: {response.has_anomaly}")
print(f"Total anomalies: {response.df['anomaly'].sum()}")
```

### API Parameters

| Parameter | Type | Description |
|-----------|------|-------------|
| `account_id` | str | Account identifier |
| `namespace` | str | Kubernetes namespace |
| `deployment` | str | Deployment name |
| `anomaly_type` | str | Metric type ('memory', 'cpu', 'error_rate') |
| `start_time` | datetime | Training data start time |
| `end_time` | datetime | Analysis end time |
| `evaluation_period` | Timedelta | Window for anomaly detection |
| `step` | str | Prometheus query step size (default: "5m") |

### Response Structure
```python
class AnomalyResponse:
    df: pd.DataFrame          # Time series data with anomaly flags
    has_anomaly: bool         # Whether any anomalies were detected
    stats: dict              # Statistical summary
```

## Algorithm Details

### Detection Flow
1. **Data Collection**: Fetch metrics from Prometheus
2. **Preprocessing**: Handle NaN values, apply forward/backward fill
3. **Constant Detection**: Skip analysis for unchanging data
4. **Model Training**: Train IsolationForest on historical data
5. **Anomaly Scoring**: Calculate anomaly scores for evaluation period
6. **Filtering**: Apply threshold and criteria-based filtering
7. **Post-processing**: Generate response with detected anomalies

### Statistical Analysis
- **IQR-based Thresholding**: Uses interquartile range for outlier detection
- **Percentile Analysis**: Calculates P99 and P99.9 statistics
- **Rolling Windows**: Applies sliding windows for baseline calculation

### Memory-Specific Logic
```python
# Dual-criteria filtering for memory metrics
change_threshold = 0.3  # 30% change required
recent_baseline = data.rolling(window=30, min_periods=10).quantile(0.5)
elevation_factor = data / recent_baseline
significant_elevation = elevation_factor >= 1.4
significant_change_filter = (percent_change >= change_threshold) & significant_elevation
```

## Configuration

### Template Structure
Each metric type has a configuration template:
```python
"memory": {
    "query_fmt": "PromQL query string",
    "time_key": "fixed",
    "time_range": "1m", 
    "default_threshold": 50 * 1024 * 1024,  # 50 MB
    "minimum_score_strength": 0.15,
    "iqr_multiplier": 2.5,
    "contamination": 0.01
}
```

### Adjustable Parameters
- **contamination**: Expected fraction of outliers (0.001-0.01)
- **minimum_score_strength**: Minimum anomaly score threshold (0.1-0.5)
- **iqr_multiplier**: IQR sensitivity multiplier (1.5-3.0)
- **default_threshold**: Minimum value threshold for analysis

## Testing

Run the test suite:
```bash
poetry run python -m unittest tests.TestAnomaly -v
```

### Test Coverage
- Basic anomaly detection functionality
- No training data scenarios
- Mock data testing with visualization
- Edge case handling

## Troubleshooting

### Common Issues

1. **No Anomalies Detected**
   - Check if data varies (constant data is filtered)
   - Verify evaluation period overlaps with available data
   - Ensure thresholds are appropriate for your metrics

2. **Too Many False Positives**
   - Increase `minimum_score_strength`
   - Increase `iqr_multiplier` 
   - Decrease `contamination` rate

3. **Timezone Errors**
   - Ensure consistent timezone handling
   - Use UTC timestamps when possible

### Debugging
Enable debug logging to see detailed analysis:
```python
import logging
logging.getLogger('server.anomaly.anomaly').setLevel(logging.DEBUG)
```

## Dependencies

- pandas >= 2.1.4
- numpy >= 1.26.2
- scikit-learn >= 1.4.1
- prometheus-api-client >= 0.5.4

## Performance Considerations

- **Memory Usage**: Large time series may require chunking
- **Processing Time**: Evaluation period filtering reduces computation
- **Model Training**: IsolationForest scales well with data size
- **Timezone Conversion**: Minimal overhead with caching

## Future Enhancements

- Node-level anomaly detection
- Additional metric types (network, disk I/O)
- Ensemble methods for improved accuracy
- Real-time streaming detection
- Adaptive threshold learning