## Predictive Models for NB

### Prerequisites

    Python
    Jupyter Notebook
    Postman

### Environment Setup

###### 1.Install dependencies

```bash
# To setup with poetry
poetry install
```

### Run server

###### Local : 
* Run `app.py` as python program
* Port forward prometheus service
    
    ```kubectl port-forward service/prometheus-kube-prometheus-prometheus 9090 -nprometheus &> /dev/null &```
  * Postman collection to get prediction
    
      ```
      {
        "info": {
            "_postman_id": "3448f6f0-d0af-4885-bb4d-1c4d3b645218",
            "name": "ML Server",
            "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
            "_exporter_id": "1799783"
        },
        "item": [
            {
                "name": "localhost:9999/predict",
                "request": {
                    "method": "POST",
                    "header": [],
                    "body": {
                        "mode": "raw",
                        "raw": "{\n    \"deployment\": \"app-dev\",\n    \"namespace\":\"nudgebee\",\n    \"container\":\"app\"\n}",
                        "options": {
                            "raw": {
                                "language": "json"
                            }
                        }
                    },
                    "url": {
                        "raw": "localhost:9999/predict",
                        "host": [
                            "localhost"
                        ],
                        "port": "9999",
                        "path": [
                            "predict"
                        ]
                    }
                },
                "response": [
                  {
                      "status": "OK",
                      "code": 200,
                      "body": "[\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:27:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:28:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:29:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:30:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:31:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:32:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:33:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:34:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:35:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:36:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:37:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:38:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:39:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:40:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:41:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:42:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:43:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:44:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:45:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:46:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:47:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:48:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:49:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:50:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:51:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:52:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:53:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:54:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:55:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:56:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:57:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:58:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 14:59:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:00:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:01:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:02:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:03:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:04:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:05:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:06:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:07:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:08:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:09:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:10:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:11:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:12:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:13:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:14:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:15:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:16:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:17:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:18:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:19:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:20:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:21:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:22:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:23:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:24:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:25:12 GMT\"\n    },\n    {\n        \"replicas\": 2,\n        \"timestamp\": \"Sat, 30 Dec 2023 15:26:12 GMT\"\n    }\n]"
                  }
                ]
            }
          ]
      } 
    ```
---

## Development Context

### Project Overview
The **ML K8s Server** is a Python-based service responsible for predictive scaling and anomaly detection for Kubernetes workloads.

### Key Technologies
- **Language:** Python 3.11+
- **Machine Learning:** Scikit-learn (linear regression for replica prediction)
- **Data Source:** Prometheus (via standard query APIs)
- **Framework:** Flask / Gunicorn

### Development Conventions
- **Build System:** Poetry (dependency management)
- **Linting:** Enforced via `black` (line-length 120), `flake8`, and `mypy`.
- **Validation:** Run `make lint` and `make test` before pushing changes.
- **Testing:** Unit and integration tests using `pytest`.
- **Predictive Model:** Uses historic CPU/Memory/Traffic metrics from Prometheus to predict future replica requirements.
