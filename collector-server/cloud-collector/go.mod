module nudgebee/collector/cloud

go 1.26.1

toolchain go1.26.10

require (
	cloud.google.com/go v0.123.0
	cloud.google.com/go/aiplatform v1.127.0
	cloud.google.com/go/auth v0.24.1
	cloud.google.com/go/auth/oauth2adapt v0.3.0
	cloud.google.com/go/bigquery v1.85.0
	cloud.google.com/go/cloudsqlconn v1.25.4
	cloud.google.com/go/cloudtasks v1.20.0
	cloud.google.com/go/compute v1.73.0
	cloud.google.com/go/container v1.55.0
	cloud.google.com/go/firestore v1.26.0
	cloud.google.com/go/functions v1.26.0
	cloud.google.com/go/logging v1.20.0
	cloud.google.com/go/monitoring v1.31.0
	cloud.google.com/go/pubsub/v2 v2.7.0
	cloud.google.com/go/recommender v1.20.0
	cloud.google.com/go/redis v1.27.0
	cloud.google.com/go/run v1.23.0
	cloud.google.com/go/scheduler v1.17.0
	cloud.google.com/go/secretmanager v1.22.0
	cloud.google.com/go/storage v1.69.0
	cloud.google.com/go/trace v1.17.0
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.23.3
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.15.0
	github.com/Azure/azure-sdk-for-go/sdk/monitor/azquery v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/advisor/armadvisor v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/alertsmanagement/armalertsmanagement v0.11.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/authorization/armauthorization/v3 v3.0.0-beta.3
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/botservice/armbotservice v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cdn/armcdn v1.1.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v4 v4.8.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cosmos/armcosmos v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/costmanagement/armcostmanagement v1.1.1
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventgrid/armeventgrid/v2 v2.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/frontdoor/armfrontdoor v1.4.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/hybridcompute/armhybridcompute/v2 v2.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault v1.5.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/logic/armlogic v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/machinelearning/armmachinelearning v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mariadb/armmariadb v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor v0.13.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/mysql/armmysqlflexibleservers v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/operationalinsights/armoperationalinsights v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/postgresql/armpostgresqlflexibleservers v1.1.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/recoveryservices/armrecoveryservices v1.6.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/redis/armredis/v3 v3.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armpolicy v1.0.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armsubscriptions v1.3.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/security/armsecurity v0.15.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/securityinsights/armsecurityinsights/v2 v2.0.0-beta.6
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/sql/armsql v1.2.0
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/storage/armstorage v1.8.1
	github.com/ClickHouse/clickhouse-go/v2 v2.49.0
	github.com/Cyprinus12138/otelgin v1.0.3
	github.com/KimMachineGun/automemlimit v1.0.0
	github.com/aws/aws-sdk-go-v2 v1.47.3
	github.com/aws/aws-sdk-go-v2/config v1.33.9
	github.com/aws/aws-sdk-go-v2/credentials v1.20.9
	github.com/aws/aws-sdk-go-v2/service/applicationautoscaling v1.51.4
	github.com/aws/aws-sdk-go-v2/service/autoscaling v1.78.4
	github.com/aws/aws-sdk-go-v2/service/backup v1.69.1
	github.com/aws/aws-sdk-go-v2/service/bedrock v1.73.4
	github.com/aws/aws-sdk-go-v2/service/cloudformation v1.82.1
	github.com/aws/aws-sdk-go-v2/service/cloudfront v1.75.0
	github.com/aws/aws-sdk-go-v2/service/cloudtrail v1.65.4
	github.com/aws/aws-sdk-go-v2/service/cloudwatch v1.73.3
	github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs v1.89.3
	github.com/aws/aws-sdk-go-v2/service/codeartifact v1.46.4
	github.com/aws/aws-sdk-go-v2/service/computeoptimizer v1.62.4
	github.com/aws/aws-sdk-go-v2/service/configservice v1.74.4
	github.com/aws/aws-sdk-go-v2/service/costandusagereportservice v1.43.4
	github.com/aws/aws-sdk-go-v2/service/costexplorer v1.74.1
	github.com/aws/aws-sdk-go-v2/service/costoptimizationhub v1.35.1
	github.com/aws/aws-sdk-go-v2/service/directconnect v1.55.1
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.70.3
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.339.0
	github.com/aws/aws-sdk-go-v2/service/ecr v1.66.4
	github.com/aws/aws-sdk-go-v2/service/ecrpublic v1.47.4
	github.com/aws/aws-sdk-go-v2/service/ecs v1.100.3
	github.com/aws/aws-sdk-go-v2/service/efs v1.49.4
	github.com/aws/aws-sdk-go-v2/service/eks v1.104.1
	github.com/aws/aws-sdk-go-v2/service/elasticache v1.63.3
	github.com/aws/aws-sdk-go-v2/service/elasticbeanstalk v1.43.4
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing v1.41.4
	github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2 v1.63.4
	github.com/aws/aws-sdk-go-v2/service/elasticsearchservice v1.51.4
	github.com/aws/aws-sdk-go-v2/service/guardduty v1.100.0
	github.com/aws/aws-sdk-go-v2/service/iam v1.64.4
	github.com/aws/aws-sdk-go-v2/service/inspector2 v1.61.3
	github.com/aws/aws-sdk-go-v2/service/kafka v1.65.4
	github.com/aws/aws-sdk-go-v2/service/kms v1.61.4
	github.com/aws/aws-sdk-go-v2/service/lambda v1.113.0
	github.com/aws/aws-sdk-go-v2/service/pi v1.46.1
	github.com/aws/aws-sdk-go-v2/service/pricing v1.49.4
	github.com/aws/aws-sdk-go-v2/service/rds v1.130.3
	github.com/aws/aws-sdk-go-v2/service/redshift v1.71.4
	github.com/aws/aws-sdk-go-v2/service/route53 v1.70.4
	github.com/aws/aws-sdk-go-v2/service/s3 v1.114.3
	github.com/aws/aws-sdk-go-v2/service/sagemaker v1.284.0
	github.com/aws/aws-sdk-go-v2/service/savingsplans v1.40.4
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.50.4
	github.com/aws/aws-sdk-go-v2/service/securityhub v1.85.0
	github.com/aws/aws-sdk-go-v2/service/ses v1.42.4
	github.com/aws/aws-sdk-go-v2/service/sfn v1.51.4
	github.com/aws/aws-sdk-go-v2/service/sns v1.47.5
	github.com/aws/aws-sdk-go-v2/service/sqs v1.52.4
	github.com/aws/aws-sdk-go-v2/service/ssm v1.79.3
	github.com/aws/aws-sdk-go-v2/service/sts v1.51.4
	github.com/aws/aws-sdk-go-v2/service/support v1.40.4
	github.com/aws/aws-sdk-go-v2/service/wafv2 v1.83.4
	github.com/aws/aws-sdk-go-v2/service/xray v1.45.4
	github.com/aws/smithy-go v1.28.5
	github.com/gin-contrib/pprof v1.5.6
	github.com/gin-gonic/gin v1.12.0
	github.com/go-logr/stdr v1.2.2
	github.com/go-playground/validator/v10 v10.30.5
	github.com/google/shlex v0.0.0-20191202100458-e7afc7fbc510
	github.com/google/uuid v1.6.0
	github.com/jmoiron/sqlx v1.4.0
	github.com/joho/godotenv v1.5.1
	github.com/json-iterator/go v1.1.12
	github.com/lib/pq v1.12.3
	github.com/microsoft/azure-devops-go-api/azuredevops/v7 v7.1.0
	github.com/microsoftgraph/msgraph-sdk-go v1.104.0
	github.com/mitchellh/mapstructure v1.5.0
	github.com/rabbitmq/amqp091-go v1.16.0
	github.com/redis/go-redis/v9 v9.23.0
	github.com/samber/lo v1.53.0
	github.com/samber/slog-formatter v1.3.0
	github.com/samber/slog-gin v1.21.1
	github.com/spf13/viper v1.21.0
	github.com/stretchr/testify v1.12.1
	github.com/wagslane/go-rabbitmq v0.16.1
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.72.0
	go.opentelemetry.io/contrib/instrumentation/runtime v0.72.0
	go.opentelemetry.io/otel v1.47.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.47.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.47.0
	go.opentelemetry.io/otel/exporters/stdout/stdoutmetric v1.47.0
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.47.0
	go.opentelemetry.io/otel/metric v1.47.0
	go.opentelemetry.io/otel/sdk v1.47.0
	go.opentelemetry.io/otel/sdk/metric v1.47.0
	go.opentelemetry.io/otel/trace v1.47.0
	go.uber.org/multierr v1.11.0
	golang.org/x/exp v0.0.0-20240719175910-8a7402abbf56
	golang.org/x/mod v0.42.0
	golang.org/x/oauth2 v0.37.0
	golang.org/x/sync v0.24.0
	google.golang.org/api v0.304.0
	google.golang.org/genproto v0.0.0-20261005182115-fad411399dd8
	google.golang.org/genproto/googleapis/api v0.0.0-20261005182115-fad411399dd8
	google.golang.org/grpc v1.84.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
)

require (
	cel.dev/expr v0.25.2 // indirect
	cloud.google.com/go/compute/metadata v0.10.0 // indirect
	cloud.google.com/go/iam v1.14.0 // indirect
	cloud.google.com/go/longrunning v1.3.0 // indirect
	cloud.google.com/go/sql v0.3.0 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.13.0 // indirect
	github.com/AzureAD/microsoft-authentication-library-for-go v1.10.1 // indirect
	github.com/ClickHouse/ch-go v0.74.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/detectors/gcp v1.35.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric v0.57.0 // indirect
	github.com/GoogleCloudPlatform/opentelemetry-operations-go/internal/resourcemapping v0.57.0 // indirect
	github.com/andybalholm/brotli v1.2.6 // indirect
	github.com/apache/arrow/go/v15 v15.0.2 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.22 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.20.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.6 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.6 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.21 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.11.7 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.13.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.20.6 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.10.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.38.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.43.4 // indirect
	github.com/bytedance/gopkg v0.1.4 // indirect
	github.com/bytedance/sonic v1.15.2 // indirect
	github.com/bytedance/sonic/loader v0.5.1 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudwego/base64x v0.1.7 // indirect
	github.com/cncf/xds/go v0.0.0-20260202195803-dba9d589def2 // indirect
	github.com/envoyproxy/go-control-plane/envoy v1.37.0 // indirect
	github.com/envoyproxy/protoc-gen-validate v1.3.3 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/fsnotify/fsnotify v1.9.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.16 // indirect
	github.com/gin-contrib/sse v1.1.1 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/go-jose/go-jose/v4 v4.1.6 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	github.com/goccy/go-json v0.10.6 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/golang-jwt/jwt/v5 v5.3.1 // indirect
	github.com/golang/groupcache v0.0.0-20241129210726-2c02b8208cf8 // indirect
	github.com/google/flatbuffers v23.5.26+incompatible // indirect
	github.com/google/s2a-go v0.1.11 // indirect
	github.com/googleapis/enterprise-certificate-proxy v0.3.23 // indirect
	github.com/googleapis/gax-go/v2 v2.26.2 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.30.0 // indirect
	github.com/klauspost/compress v1.19.1 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/leodido/go-urn v1.5.0 // indirect
	github.com/mattn/go-isatty v0.0.23 // indirect
	github.com/microsoft/kiota-abstractions-go v1.9.4 // indirect
	github.com/microsoft/kiota-authentication-azure-go v1.3.1 // indirect
	github.com/microsoft/kiota-http-go v1.5.6 // indirect
	github.com/microsoft/kiota-serialization-form-go v1.1.3 // indirect
	github.com/microsoft/kiota-serialization-json-go v1.1.2 // indirect
	github.com/microsoft/kiota-serialization-multipart-go v1.1.2 // indirect
	github.com/microsoft/kiota-serialization-text-go v1.1.3 // indirect
	github.com/microsoftgraph/msgraph-sdk-go-core v1.4.1 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/paulmach/orb v0.13.0 // indirect
	github.com/pbnjay/memory v0.0.0-20210728143218-7b4eea64cf58 // indirect
	github.com/pelletier/go-toml/v2 v2.4.3 // indirect
	github.com/pierrec/lz4/v4 v4.1.27 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
	github.com/planetscale/vtprotobuf v0.6.1-0.20240319094008-0393e58bdf10 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.60.0 // indirect
	github.com/sagikazarmark/locafero v0.11.0 // indirect
	github.com/samber/slog-common v0.21.0 // indirect
	github.com/samber/slog-multi v1.8.0 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/shopspring/decimal v1.5.0 // indirect
	github.com/sourcegraph/conc v0.3.1-0.20240121214520-5f936abd7ae8 // indirect
	github.com/spf13/afero v1.15.0 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/spf13/pflag v1.0.10 // indirect
	github.com/spiffe/go-spiffe/v2 v2.8.1 // indirect
	github.com/std-uritemplate/std-uritemplate/go/v2 v2.0.3 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.3.1 // indirect
	github.com/zeebo/xxh3 v1.1.0 // indirect
	go.mongodb.org/mongo-driver/v2 v2.8.0 // indirect
	go.opencensus.io v0.24.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/detectors/gcp v1.45.0 // indirect
	go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc v0.72.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.0 // indirect
	go.uber.org/atomic v1.12.0 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/arch v0.29.0 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/telemetry v0.0.0-20260924152758-ed294f943157 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/time v0.16.0 // indirect
	golang.org/x/tools v0.51.0 // indirect
	golang.org/x/xerrors v0.0.0-20240903120638-7835f813f4da // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20261005182115-fad411399dd8 // indirect
)
