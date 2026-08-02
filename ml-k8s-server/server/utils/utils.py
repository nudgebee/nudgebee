import json
import logging
import os
import tempfile
from datetime import datetime, timedelta
from typing import Any, Optional

import pandas as pd
import requests
from opentelemetry import metrics, trace
from opentelemetry.instrumentation.system_metrics import SystemMetricsInstrumentor
from opentelemetry.sdk.metrics import MeterProvider
from opentelemetry.sdk.metrics.export import PeriodicExportingMetricReader
from opentelemetry.sdk.resources import Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import ConsoleSpanExporter, SimpleSpanProcessor
from requests.adapters import HTTPAdapter
from sqlalchemy import create_engine, text
from sqlalchemy.engine import Engine
from urllib3 import Retry
from dotenv import load_dotenv

load_dotenv()


logger = logging.getLogger(__name__)

# HTTP status codes that warrant retrying an outbound request.
RETRY_STATUS_CODES = (429, 500, 502, 503, 504)

default_health_check_endpoints = [
    "/actuator/health",
    "/healthcheck",
    "/health",
    "/status",
    "/health/live",
    "/health/ready",
    "/ping",
    "/check",
    "/alive",
    "/readiness",
    "/probe",
    "/heartbeat",
    "/statuscheck",
    "/diagnostics",
    "/isalive",
    "/isready",
    "/probe/health",
    "/probe/liveness",
    "/probe/readiness",
    "/probe/heartbeat",
    "/probe/status",
    "/probe/alive",
    "/probe/check",
    "/api/health",
    "/healthz",
    "/livez",
]


def handle_df_for_nan(df: pd.DataFrame) -> pd.DataFrame:
    df.fillna(0, inplace=True)
    return df


def get_model_name(account_id: str, tenant_id: str, namespace: str, deployment_name: str, model_type: str) -> str:
    return f"{tenant_id}_{account_id}_{namespace}_{deployment_name}_{model_type}.keras"


def get_model_path(account_id: str, tenant_id: str, namespace: str, deployment_name: str) -> str:
    return f"{tenant_id}/{account_id}/{namespace}/{deployment_name}/"


def get_http_session_with_retry(retry_attempts: int = 3, backoff_factor: float = 1) -> requests.Session:
    # Configure retry settings for HTTP requests
    retry_strategy = None
    if retry_attempts > 0:
        retry_strategy = Retry(
            total=retry_attempts,  # Total number of retries
            status_forcelist=RETRY_STATUS_CODES,  # Retry on these HTTP status codes
            allowed_methods=["HEAD", "GET", "OPTIONS", "POST"],  # Only retry on these HTTP methods
            backoff_factor=backoff_factor,  # Backoff factor for delay between retries
        )
    # Create a session with the retry strategy
    session = requests.Session()
    session.mount("https://", HTTPAdapter(max_retries=retry_strategy))
    session.mount("http://", HTTPAdapter(max_retries=retry_strategy))
    return session


def set_metrics_exporter() -> None:
    resource_attr = (
        {
            key.strip(): str(value.strip())
            for key, value in (item.split("=") for item in OTELConfig.OTEL_RESOURCE_ATTRIBUTES.split(","))
        }
        if OTELConfig.OTEL_RESOURCE_ATTRIBUTES
        else {}
    )
    if OTELConfig.service_name:
        resource_attr["service.name"] = OTELConfig.service_name
        resource_attr["application"] = OTELConfig.service_name

    otl_resource = Resource.create(resource_attr)
    exporters = OTELConfig.OTEL_EXPORTER.split(",")
    if "otlp" in exporters:
        if not OTELConfig.OTEL_EXPORTER_OTLP_ENDPOINT:
            raise ValueError(
                "OTLP exporter(OTEL_EXPORTER) is enabled but OTLP "
                "endpoint(OTEL_EXPORTER_OTLP_ENDPOINT) is not provided"
            )
        end_point = OTELConfig.OTEL_EXPORTER_OTLP_ENDPOINT.rstrip("/")
        if OTELConfig.OTEL_EXPORTER_OTLP_PROTOCOL == "grpc":
            from opentelemetry.exporter.otlp.proto.grpc.metric_exporter import OTLPMetricExporter

            otlp_metrics_exporter = OTLPMetricExporter(endpoint=end_point, insecure=True, timeout=5)
        else:
            if not end_point.endswith("/v1/metrics"):
                end_point += "/v1/metrics"
            from opentelemetry.exporter.otlp.proto.http.metric_exporter import OTLPMetricExporter  # type: ignore

            otlp_metrics_exporter = OTLPMetricExporter(endpoint=end_point, timeout=5)
        metric_reader = PeriodicExportingMetricReader(otlp_metrics_exporter)
        provider = MeterProvider(metric_readers=[metric_reader], resource=otl_resource)
        metrics.set_meter_provider(provider)
        meter = metrics.get_meter(__name__)
        SystemMetricsInstrumentor().instrument(meter=meter)


def get_trace_provider(service_name: str) -> TracerProvider:
    # fetching resource attr else returning empty dict
    resource_attr = (
        {
            key.strip(): str(value.strip())
            for key, value in (item.split("=") for item in OTELConfig.OTEL_RESOURCE_ATTRIBUTES.split(","))
        }
        if OTELConfig.OTEL_RESOURCE_ATTRIBUTES
        else {}
    )
    if service_name:
        resource_attr["service.name"] = service_name
        resource_attr["application"] = service_name
    otl_resource = Resource.create(resource_attr)
    provider = TracerProvider(resource=otl_resource)
    exporters = OTELConfig.OTEL_EXPORTER.split(",")
    if "otlp" in exporters:
        if not OTELConfig.OTEL_EXPORTER_OTLP_ENDPOINT:
            raise ValueError(
                "OTLP exporter(OTEL_EXPORTER) is enabled but OTLP trace "
                "endpoint(OTEL_EXPORTER_OTLP_ENDPOINT) is not provided"
            )
        end_point = OTELConfig.OTEL_EXPORTER_OTLP_ENDPOINT.rstrip("/")
        if OTELConfig.OTEL_EXPORTER_OTLP_PROTOCOL == "grpc":
            from opentelemetry.exporter.otlp.proto.grpc.trace_exporter import OTLPSpanExporter

            otlp_exporter = OTLPSpanExporter(endpoint=end_point, insecure=True, timeout=5)
        else:
            if not end_point.endswith("/v1/traces"):
                end_point += "/v1/traces"
            from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter  # type: ignore

            otlp_exporter = OTLPSpanExporter(endpoint=end_point, timeout=5)
        provider.add_span_processor(SimpleSpanProcessor(otlp_exporter))
    if "console" in exporters:
        console_exporter = ConsoleSpanExporter(formatter=lambda span: json.dumps(json.loads(span.to_json(indent=0))))
        provider.add_span_processor(SimpleSpanProcessor(console_exporter))
    return provider


def set_global_trace() -> None:
    trace.set_tracer_provider(get_trace_provider(OTELConfig.service_name))


def get_trace(name: str) -> trace.Tracer:
    return trace.get_tracer(name)


class DBConfig:
    url = os.environ.get("ML_INFERENCE_DATABASE_URL", "")
    if not url:
        raise ValueError("ML_INFERENCE_DATABASE_URL environment variable not set")
    table_name = os.environ.get("ML_INFERENCE_TABLE_NAME", "ml_inference")
    pool_size = int(os.environ.get("ML_DB_POOL_SIZE", 5))
    max_overflow = int(os.environ.get("ML_DB_MAX_OVERFLOW", 5))
    pool_recycle = int(os.environ.get("ML_DB_POOL_RECYCLE", 1800))


class QueueConfig:
    ML_WORKER_MAX_PRIORITY: int = 10
    ML_RECOMMENDATION_QUEUE = os.environ.get("ML_RECOMMENDATION_QUEUE", "ml-recommendation")
    ML_RECOMMENDATION_EXCHANGE = os.environ.get("ML_RECOMMENDATION_EXCHANGE", "ml-recommendation")
    RABBIT_MQ_HOST = os.environ.get("RABBIT_MQ_HOST", "localhost")
    RABBIT_MQ_PORT = os.environ.get("RABBIT_MQ_PORT", "5672")
    RABBIT_MQ_USERNAME = os.environ.get("RABBIT_MQ_USERNAME", "guest")
    RABBIT_MQ_PASSWORD = os.environ.get("RABBIT_MQ_PASSWORD", "guest")


class FileConfig:
    base_path = os.environ.get("ML_FILE_BASE_PATH", tempfile.gettempdir())


class RecommendationConfig:
    table_name = os.environ.get("ML_RECOMMENDATION_TABLE_NAME", "recommendation")


class ModelConfig:
    prediction_step = os.environ.get("ML_PREDICTION_STEP", 168)
    prediction_initial_set = os.environ.get("ML_PREDICTION_INITIAL_SET", 24)
    epoch = os.environ.get("ML_RUN_EPOCHS", 5)
    batch_size = os.environ.get("ML_RUN_BATCH_SIZE", 64)
    validation_split = os.environ.get("ML_RUN_VALIDATION_SPLIT", 0.20)
    verbose = os.environ.get("ML_RUN_VERBOSE", 1)
    nan_percentage = os.environ.get("ML_MODEL_INPUT_NAN_PERCENTAGE", 15)
    zero_percentage = os.environ.get("ML_MODEL_INPUT_ZERO_PERCENTAGE", 15)
    model_store = os.environ.get("ML_MODEL_STORE_PROVIDER", "file")
    bucket_name = os.environ.get("ML_MODEL_STORE_BUCKET", "")
    if (model_store != "file") and not bucket_name:
        raise ValueError("ML_MODEL_STORE_BUCKET environment variable not set")
    non_zero_columns = os.environ.get("ML_MODEL_NON_ZERO_COLUMNS", "replicas")


class MetricsServerConfigs:
    url = os.environ.get("RELAY_SERVER_ENDPOINT", "http://localhost:8080")
    if not url:
        raise ValueError("RELAY_SERVER_ENDPOINT environment variable not set")
    url = url + "request" if url.endswith("/") else url + "/request"
    duration = os.environ.get("ML_METRICS_DURATION_DAYS", 7)
    time_range = os.environ.get("ML_METRICS_DURATION_TIME_RANGE", "1h")
    secret = os.environ.get("RELAY_SERVER_SECRET_KEY", "")
    if not secret:
        raise ValueError("RELAY_SERVER_SECRET_KEY environment variable not set")
    __health_checks = os.getenv("APP_HEALTH_ENDPOINTS", None)
    if __health_checks:
        additional_endpoints = __health_checks.split(",")
        default_health_check_endpoints.extend(additional_endpoints)
    health_check_endpoints = list(set(default_health_check_endpoints))


class OTELConfig:
    OTEL_EXPORTER_OTLP_ENDPOINT: str = os.environ.get("OTEL_EXPORTER_OTLP_ENDPOINT", "")
    OTEL_EXPORTER: str = os.environ.get("OTEL_EXPORTER", "")
    OTEL_EXPORTER_OTLP_PROTOCOL: str = os.environ.get("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")
    OTEL_RESOURCE_ATTRIBUTES: str = os.environ.get("OTEL_RESOURCE_ATTRIBUTES", "")
    service_name = os.environ.get("OTEL_SERVICE_NAME", "ml-k8s-server")


class ScrubConfig:
    """Configuration for the universal data-scrubbing API.

    Tier-1 regex is always applied when ``enabled``. Tier-2 NER is requested
    per HTTP call but uses the model/lang configured here. ``reversible_types``
    is the set of soft-PII types eligible for reversible tokenisation;
    secrets are always irreversible regardless.
    """

    enabled = os.environ.get("SCRUB_ENABLED", "true").lower() == "true"
    ner_model = os.environ.get("SCRUB_NER_MODEL", "en_core_web_sm")
    ner_lang = os.environ.get("SCRUB_NER_LANG", "en")
    # Comma-separated subset of PERSON,LOCATION,EMAIL,PHONE.
    reversible_types = frozenset(
        t.strip().upper()
        for t in os.environ.get("SCRUB_REVERSIBLE_TYPES", "PERSON,LOCATION,EMAIL,PHONE").split(",")
        if t.strip()
    )
    # Regexes (matched against an NER span's text) that mark a hit as an
    # infrastructure identifier to PRESERVE rather than redact — whole-token
    # cases like deployments/hosts named after envs or DNS. Sub-token cases
    # (a name inside a compound id) are handled separately in the scrubber.
    # Override with SCRUB_INFRA_ALLOWLIST (pipe-separated regexes).
    #
    # Additions 2026-08-01 driven by curl evidence against dev — on a
    # user's "hello" turn the scrubber found 47 hits, dominated by:
    #   - spaCy tagging bare ops-tool names ("grafana") as PERSON
    #   - spaCy tagging bare English infra vocab ("node") as LOCATION
    # These require whole-span allowlist entries (the existing entries
    # only match tokens that already have a separator or a suffix like
    # "-prod", so bare words fall through).
    _default_infra_allowlist = [
        # Env-suffixed identifiers: nginx-prod, api-staging, checkout-canary.
        r"(?i).*-(prod|dev|develop|staging|stage|qa|uat|canary|test|sandbox)\b",
        # DNS-shaped infra hostnames: my-svc.svc.cluster.local, api.internal.
        r"(?i).*\.(svc|local|internal|cluster\.local)\b",
        # k8s owner-ref prefixes: pod/nginx, deployment.apps/api, ns/prod.
        r"(?i)^(pod|deployment|deployment\.apps|replicaset|statefulset|daemonset"
        r"|cronjob|job|ns|namespace|svc|service|node)/",
        # Well-known ops / observability tool names — spaCy routinely tags
        # these as PERSON/LOCATION on SRE text ("grafana" observed hitting
        # PERSON in dev; "prometheus" and "alertmanager" happen to be safe
        # today but not guaranteed across model versions, so allowlist them
        # explicitly).
        r"(?i)^(grafana|prometheus|alertmanager|nginx|httpd|apache|"
        r"mysql|postgres(?:ql)?|mongo(?:db)?|redis|memcached|kafka|zookeeper|"
        r"rabbitmq|elasticsearch|opensearch|kibana|logstash|fluent(?:d|bit)|"
        r"jaeger|zipkin|envoy|istio|argo(?:cd)?|linkerd|consul|vault|nomad|"
        r"traefik|calico|cilium|weave|coredns|kube-proxy|kubelet|"
        r"kube-apiserver|etcd|"
        # Additions 2026-08-02 measured on session 17276ae7 remainders:
        # Loki, Cortex, Honeycomb (all NER-tagged despite being obs tools).
        r"loki|tempo|mimir|thanos|cortex|victoria(?:metrics)?|"
        r"chronosphere|honeycomb|dynatrace|appdynamics|wavefront|"
        r"splunk|datadog|sentry|pagerduty|opsgenie|victorops|xmatters|"
        r"cloudwatch|stackdriver|sumo(?:logic)?)$",
        # Bare k8s / cloud vocabulary words that spaCy mis-tags as
        # LOCATION or PERSON when they appear standalone in English text
        # ("check node ip-...", "restart pod X").
        r"(?i)^(node|pod|service|cluster|namespace|container|deployment|"
        r"replica(?:set)?|statefulset|daemonset|cronjob|job|ingress|"
        r"configmap|secret|volume|region|zone|az)$",
        # Additions 2026-08-02 driven by empirical /scrub run against the
        # k8s_orchestrator_lean system-prompt corpus (55KB). 16 of the
        # observed PERSON/LOCATION hits were false-fires on ops vocabulary
        # NOT covered by the existing entries above. The extensions below
        # are curated to preserve zero recall on real names — every
        # regex is a whole-token match, and case-sensitive where the
        # false-fire pattern is UPPERCASE (SQL keywords, short acronyms,
        # k8s state names). Ambiguous English words that ARE also given
        # names (Mark, Max, Running as name) were deliberately NOT added.
        #
        # CI/CD, VCS, build, container, IaC tools. False-fires observed:
        # Git → PERSON_1, Helm → PERSON_14, k8s / K8s → PERSON_6/PERSON_13.
        # Case-insensitive whole-token — "GitHub Actions" (two-token span)
        # is not matched by ^github$.
        r"(?i)^(git|github|gitlab|bitbucket|helm|kubectl|oc|crictl|"
        r"kustomize|k8s|k3s|k9s|kind|minikube|rancher|openshift|"
        r"docker|podman|buildah|kaniko|containerd|"
        r"terraform|tofu|pulumi|ansible|chef|puppet|jenkins|tekton|"
        r"drone|circleci|spinnaker|flux|"
        r"npm|pnpm|yarn|pip|poetry|cargo|gradle|maven|bazel|make|cmake|"
        r"vim|nvim|emacs)$",
        # Data-format / protocol / short-acronym vocabulary — same class
        # of bare-word false-fires (JSON → PERSON_3, DB → PERSON_9,
        # METADATA → PERSON_2, H1 → PERSON_7 in the corpus). Upper-case
        # whole-token only — real names never look like "JSON".
        r"^(JSON|XML|YAML|YML|HCL|TOML|INI|CSV|TSV|HTML|CSS|SCSS|"
        r"SQL|DDL|DML|CRUD|REST|GraphQL|gRPC|RPC|API|SDK|CLI|TUI|GUI|"
        r"HTTP|HTTPS|TCP|UDP|TLS|SSL|SSH|DNS|DHCP|NTP|SMTP|IMAP|"
        r"FTP|SFTP|JWT|OAuth|SAML|LDAP|MFA|SSO|OTP|RBAC|ACL|"
        r"DB|IP|IO|OS|UI|UX|QA|VM|VPC|VPN|CDN|CORS|CSRF|CSP|"
        r"DTO|POJO|ETL|ELT|OLAP|OLTP|CQRS|BFF|SPA|SSR|SSG|"
        r"H1|H2|H3|H4|H5|H6|METADATA)$",
        # SQL keywords as bare UPPERCASE tokens — the exact false-fire
        # pattern in query plans / prompt examples (EXPLAIN → PERSON_11
        # in the corpus). Zero recall risk (no real name is "SELECT").
        r"^(SELECT|INSERT|UPDATE|DELETE|EXPLAIN|ANALYZE|VACUUM|"
        r"CREATE|DROP|ALTER|TRUNCATE|COMMIT|ROLLBACK|SAVEPOINT|"
        r"BEGIN|GRANT|REVOKE|DECLARE|RETURN|RETURNS|"
        r"JOIN|INNER|OUTER|LEFT|RIGHT|CROSS|WHERE|FROM|GROUP|BY|"
        r"ORDER|LIMIT|OFFSET|HAVING|UNION|INTERSECT|EXCEPT|EXISTS|"
        r"CASCADE|RESTRICT|COALESCE|CAST|WITH|RECURSIVE|OVER|"
        r"PARTITION|WINDOW|LATERAL|LANGUAGE|IMMUTABLE|VOLATILE|STABLE|"
        r"NULL|TRUE|FALSE|AND|OR|NOT|IN|IS|AS|ON)$",
        # K8s pod-lifecycle state / event names — CamelCase compound
        # tokens that spaCy tags as LOCATION on their own
        # (CrashLoopBackOff → LOCATION_1, OOMKilled → LOCATION_2 in the
        # corpus). Case-sensitive: these are literals from kubectl
        # output, not free-text English.
        r"^(CrashLoopBackOff|OOMKilled|ImagePullBackOff|ErrImagePull|"
        r"ContainerCreating|PodInitializing|Terminating|Pending|"
        r"Succeeded|Failed|Completed|Evicted|Preempted|"
        r"CreateContainerConfigError|CreateContainerError|"
        r"RunContainerError|InvalidImageName|KillingContainer|"
        r"RegisteredNode|NodeNotReady|NodeReady|"
        r"NodeHasSufficientMemory|NodeHasNoDiskPressure|"
        r"NodeHasSufficientPID|BackOff|Unhealthy|Killing|Pulling|"
        r"Pulled|Started|Created|Scheduled|SuccessfulCreate|"
        r"SuccessfulDelete|FailedScheduling|FailedMount|"
        r"FailedAttachVolume|FailedCreatePodSandBox|"
        # Additions 2026-08-02 measured on session 17276ae7:
        # ContainerStatusUnknown seen as PERSON in dev.
        r"ContainerStatusUnknown|ContainerCannotRun|ImageInspectError)$",
        # Multi-word cloud service names — spaCy tags "Cloud Logging"
        # etc. as a single PERSON span. Case-sensitive because the
        # provider-branded forms are always Title-Case ("cloud storage"
        # lowercase is generic English). Covers the GCP "Cloud X" family
        # + a handful of well-known compound names across clouds.
        r"^(Cloud (?:Logging|SQL|Storage|Run|Functions|Trace|Monitoring|"
        r"Build|Spanner|Bigtable|Dataflow|Composer|Armor|NAT|CDN|DNS|"
        r"KMS|IAM|Pub/Sub|Datastore|Tasks|Scheduler|Endpoints|Endpoint|"
        r"Load Balancing|Interconnect|Router|VPN)|"
        r"New Relic|Sumo Logic|Elastic Cloud|Grafana Cloud|"
        r"Elastic Beanstalk|CloudFront Origin|"
        r"Kafka (?:Connect|Streams|Broker)|"
        r"Cosmos DB|Service Bus|Event Grid|Event Hub|Blob Storage|"
        r"App Service|Application Insights|Log Analytics|"
        r"Container Instances|Container Apps|Container Registry|"
        r"App Engine)$",
        # Linux distribution / release codenames — spaCy tags these as
        # LOCATION (Bullseye, Bookworm are Debian releases; Bionic /
        # Focal / Jammy are Ubuntu). Compound "Debian Bookworm" is
        # spanned as PERSON.
        r"(?i)^(bullseye|bookworm|trixie|sid|buster|stretch|jessie|wheezy|" r"squeeze|"
        # Ubuntu release codenames — bare word or with "Ubuntu" prefix.
        r"noble|mantic|jammy|focal|bionic|xenial|trusty|precise|"
        # Fedora / RHEL / Alpine — codenames + short "el7/el8" family.
        r"alpine|centos|rocky|almalinux|amazonlinux|"
        r"debian bookworm|debian bullseye|debian trixie|debian sid|"
        r"debian buster|debian stretch|"
        r"ubuntu noble|ubuntu jammy|ubuntu focal|ubuntu bionic)$",
    ]
    infra_allowlist = [
        p for p in os.environ.get("SCRUB_INFRA_ALLOWLIST", "").split("|") if p.strip()
    ] or _default_infra_allowlist


class DatabaseEngine:
    _instance: Optional[Engine] = None

    @staticmethod
    def get_engine() -> Engine:
        if DatabaseEngine._instance is None:
            DatabaseEngine._instance = create_engine(
                DBConfig.url,
                pool_size=DBConfig.pool_size,
                max_overflow=DBConfig.max_overflow,
                pool_recycle=DBConfig.pool_recycle,
                pool_pre_ping=True,
                isolation_level="AUTOCOMMIT",
            )
        return DatabaseEngine._instance


SQL_QUERY = "SELECT value FROM cloud_account_attrs WHERE cloud_account_id = :account_id AND UPPER(name) = :name"


def get_prediction_duration(account_id: str) -> int:
    try:
        engine = DatabaseEngine.get_engine()
        with engine.connect() as conn:
            result = conn.execute(text(SQL_QUERY), {"account_id": account_id, "name": "ML_METRICS_DURATION_DAYS"})
            for row in result:
                return int(row[0])
    except Exception as e:
        logger.warning(f"Failed to get prediction duration from DB for account:{account_id}, {e}")
    return int(MetricsServerConfigs.duration)


def get_prediction_time_range(account_id: str) -> str:
    try:
        engine = DatabaseEngine.get_engine()
        with engine.connect() as conn:
            result = conn.execute(text(SQL_QUERY), {"account_id": account_id, "name": "ML_METRICS_DURATION_TIME_RANGE"})
            for row in result:
                return str(row[0])
    except Exception as e:
        logger.warning(f"Failed to get prediction time range from DB for account:{account_id}, {e}")
    return MetricsServerConfigs.time_range


def get_prediction_step(account_id: str) -> int:
    try:
        engine = DatabaseEngine.get_engine()
        with engine.connect() as conn:
            result = conn.execute(text(SQL_QUERY), {"account_id": account_id, "name": "ML_PREDICTION_STEP"})
            for row in result:
                return int(row[0])
    except Exception as e:
        logger.warning(f"Failed to get prediction step from DB for account:{account_id}, {e}")
    return int(ModelConfig.prediction_step)


def get_prediction_initial_set(account_id: str) -> int:
    try:
        engine = DatabaseEngine.get_engine()
        with engine.connect() as conn:
            result = conn.execute(text(SQL_QUERY), {"account_id": account_id, "name": "ML_PREDICTION_INITIAL_SET"})
            for row in result:
                return int(row[0])
    except Exception as e:
        logger.warning(f"Failed to get prediction initial set from DB for account:{account_id}, {e}")
    return int(ModelConfig.prediction_initial_set)


def get_prediction_run_batch_size(account_id: str) -> int:
    try:
        engine = DatabaseEngine.get_engine()
        with engine.connect() as conn:
            result = conn.execute(text(SQL_QUERY), {"account_id": account_id, "name": "ML_RUN_BATCH_SIZE"})
            for row in result:
                return int(row[0])
    except Exception as e:
        logger.warning(f"Failed to get prediction run batch size from DB for account:{account_id}, {e}")
    return int(ModelConfig.batch_size)


def generate_hourly_dict(
    start_time: datetime, end_time: datetime, default_value: Any = None, duration_in_days: int | None = None
) -> dict:
    hourly_dict = {}
    if duration_in_days:
        start_time = end_time.replace(hour=0, minute=0, second=0, microsecond=0) - timedelta(days=duration_in_days)
    current = start_time.replace(minute=0, second=0, microsecond=0)

    while current <= end_time:
        hourly_dict[current] = default_value
        current += timedelta(hours=1)

    return hourly_dict
