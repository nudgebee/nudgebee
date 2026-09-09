# SWE-bench pipeline runner.
#
# Replaces the ConfigMap-mounted-scripts arrangement, which was fine for a
# one-off and does not survive the dataset growing: the 500-instance task set is
# 21 MB and a ConfigMap caps at ~1 MB, which is why the earlier runner had to
# ship a gzipped, field-stripped copy of the dataset instead of the real thing.
#
# kubectl is here because grade.sh submits one Job per instance and collect.py
# reads pod logs. Paired with k8s/rbac.yaml, the pipeline no longer borrows a
# developer's kubeconfig.
FROM python:3.12-slim

ARG KUBECTL_VERSION=v1.31.0

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl git \
    && curl -fsSLo /usr/local/bin/kubectl \
       "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/amd64/kubectl" \
    && chmod +x /usr/local/bin/kubectl \
    && apt-get purge -y curl && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
ENV PYTHONPATH=/app

# swebench brings the official reporting code. `docker` comes with it and is
# imported by swebench.harness.reporting at module load; we always pass
# client=None, so the library is needed but no daemon is.
#
# Pinned to the version that produced the figures we validated and reconciled
# against the Job outcomes. This is only the *aggregator* — the per-instance
# verdict comes from each task's own test.sh, which pins its own swebench via uv
# inside the grading container and is unaffected by this.
#
# Do not float this pin: the aggregator decides how instances are bucketed, so a
# silent version bump can move the reported rate without the agent changing.
RUN pip install --no-cache-dir swebench==5.0.1 \
    sqlalchemy==2.0.36 psycopg2-binary==2.9.10

# Build context is llm/benchmark (not swebench/) so the benchmark_server
# models can be reused verbatim instead of the schema being redeclared here.
# Build with:  docker buildx build -f swebench/Dockerfile llm/benchmark
COPY swebench/run_swebench.py swebench/make_predictions.py /app/
COPY swebench/k8s/grade.sh swebench/k8s/collect.py swebench/k8s/persist.py swebench/k8s/pipeline.sh /app/k8s/
COPY benchmark_server/models /app/benchmark_server/models
RUN touch /app/benchmark_server/__init__.py \
    && chmod +x /app/k8s/grade.sh /app/k8s/pipeline.sh

# Task packages: test.sh and config.json per instance, needed to grade.
# NOTE: each package also carries solution/solve.sh — the gold patch. This image
# is the *scorer*, never the agent; the agent runs in a separate workspace pod
# and must never be given this path.
COPY swebench/dataset /app/dataset

# Frozen instance lists. Predict and score read the same file — see the guard in
# run_swebench.py.
COPY swebench/frozen_*.txt /app/

ENTRYPOINT ["/app/k8s/pipeline.sh"]
