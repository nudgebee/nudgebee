module nudgebee/services

go 1.26.1

require (
	github.com/ClickHouse/clickhouse-go/v2 v2.32.0
	github.com/Cyprinus12138/otelgin v1.0.2
	github.com/DATA-DOG/go-sqlmock v1.5.2
	github.com/JohannesKaufmann/html-to-markdown/v2 v2.4.0
	github.com/PagerDuty/go-pagerduty v1.8.0
	github.com/agiledragon/gomonkey/v2 v2.14.0
	github.com/alecthomas/participle/v2 v2.1.4
	github.com/allegro/bigcache/v3 v3.1.0
	github.com/andygrunwald/go-jira v1.17.0
	github.com/aws/aws-sdk-go-v2 v1.41.6
	github.com/aws/aws-sdk-go-v2/config v1.32.7
	github.com/aws/aws-sdk-go-v2/credentials v1.19.7
	github.com/aws/aws-sdk-go-v2/service/cognitoidentity v1.33.23
	github.com/aws/aws-sdk-go-v2/service/cognitoidentityprovider v1.59.2
	github.com/aws/aws-sdk-go-v2/service/ec2 v1.298.0
	github.com/aws/aws-sdk-go-v2/service/marketplaceentitlementservice v1.36.2
	github.com/aws/aws-sdk-go-v2/service/marketplacemetering v1.34.1
	github.com/aws/aws-sdk-go-v2/service/pricing v1.41.1
	github.com/aws/aws-sdk-go-v2/service/sqs v1.42.25
	github.com/aws/aws-sdk-go-v2/service/sts v1.41.9
	github.com/eko/gocache/lib/v4 v4.2.3
	github.com/eko/gocache/store/bigcache/v4 v4.2.4
	github.com/eko/gocache/store/redis/v4 v4.2.6
	github.com/fatih/structs v1.1.0
	github.com/gin-contrib/pprof v1.4.0
	github.com/gin-gonic/gin v1.10.1
	github.com/go-playground/validator/v10 v10.30.1
	github.com/golang-jwt/jwt/v4 v4.5.2
	github.com/golang-jwt/jwt/v5 v5.3.0
	github.com/google/go-github/v61 v61.0.0
	github.com/google/uuid v1.6.0
	github.com/graphql-go/graphql v0.8.1
	github.com/jmoiron/sqlx v1.4.0
	github.com/lib/pq v1.12.3
	github.com/michaeldcanady/servicenow-sdk-go v1.7.1
	github.com/mitchellh/mapstructure v1.5.0
	github.com/noirbizarre/gonja v0.0.0-20200629003239-4d051fd0be61
	github.com/nudgebee/logparser v0.0.0-20251218094028-bf4256a63b22
	github.com/prometheus/prometheus v0.35.0
	github.com/redis/go-redis/v9 v9.13.0
	github.com/samber/lo v1.53.0
	github.com/samber/slog-formatter v1.3.0
	github.com/samber/slog-gin v1.21.0
	github.com/spf13/viper v1.18.2
	github.com/stretchr/testify v1.11.1
	github.com/swaggo/files v1.0.1
	github.com/swaggo/gin-swagger v1.6.1
	github.com/swaggo/swag v1.16.6
	github.com/wagslane/go-rabbitmq v0.15.0
	github.com/xuri/excelize/v2 v2.10.0
	gitlab.com/gitlab-org/api/client-go v1.46.0
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.68.0
	go.opentelemetry.io/contrib/instrumentation/runtime v0.54.0
	go.opentelemetry.io/otel v1.43.0
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc v1.29.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc v1.42.0
	go.opentelemetry.io/otel/exporters/stdout/stdoutmetric v1.42.0
	go.opentelemetry.io/otel/exporters/stdout/stdouttrace v1.42.0
	go.opentelemetry.io/otel/metric v1.43.0
	go.opentelemetry.io/otel/sdk v1.43.0
	go.opentelemetry.io/otel/sdk/metric v1.43.0
	go.opentelemetry.io/otel/trace v1.43.0
	golang.org/x/sync v0.20.0
	google.golang.org/grpc v1.79.3
	gopkg.in/yaml.v2 v2.4.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/Azure/azure-sdk-for-go/sdk/azcore v1.21.0
	github.com/Azure/azure-sdk-for-go/sdk/azidentity v1.13.1
	github.com/Azure/azure-sdk-for-go/sdk/internal v1.11.2 // indirect
	github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription v1.2.0
	github.com/AzureAD/microsoft-authentication-library-for-go v1.6.0 // indirect
	github.com/kylelemons/godebug v1.1.0 // indirect
	github.com/pkg/browser v0.0.0-20240102092130-5ac0b6a4141c // indirect
)

require (
	github.com/JohannesKaufmann/dom v0.2.0 // indirect
	github.com/KyleBanks/depth v1.2.1 // indirect
	github.com/PuerkitoBio/purell v1.1.1 // indirect
	github.com/PuerkitoBio/urlesc v0.0.0-20170810143723-de5bf2ad4578 // indirect
	github.com/alecthomas/repr v0.5.1 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.18.17 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.4.22 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.7.22 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.4 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.8 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.13.22 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.0.5 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.30.9 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.35.13 // indirect
	github.com/aws/smithy-go v1.25.0 // indirect
	github.com/bytedance/sonic/loader v0.1.1 // indirect
	github.com/cenkalti/backoff/v4 v4.3.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cloudwego/base64x v0.1.4 // indirect
	github.com/cloudwego/iasm v0.2.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/go-openapi/jsonpointer v0.19.5 // indirect
	github.com/go-openapi/jsonreference v0.19.6 // indirect
	github.com/go-openapi/spec v0.20.4 // indirect
	github.com/go-openapi/swag v0.21.1 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/goph/emperror v0.17.1 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.28.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.8 // indirect
	github.com/hashicorp/golang-lru/v2 v2.0.7 // indirect
	github.com/jaeyo/go-drain3 v0.1.2 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/microsoft/kiota-abstractions-go v1.9.2 // indirect
	github.com/microsoft/kiota-http-go v1.5.3 // indirect
	github.com/microsoft/kiota-serialization-form-go v1.1.2 // indirect
	github.com/microsoft/kiota-serialization-json-go v1.1.2 // indirect
	github.com/microsoft/kiota-serialization-multipart-go v1.1.2 // indirect
	github.com/microsoft/kiota-serialization-text-go v1.1.2 // indirect
	github.com/mozillazg/go-httpheader v0.4.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/richardlehane/mscfb v1.0.4 // indirect
	github.com/richardlehane/msoleps v1.0.4 // indirect
	github.com/samber/slog-common v0.21.0 // indirect
	github.com/sirupsen/logrus v1.9.3 // indirect
	github.com/std-uritemplate/std-uritemplate/go/v2 v2.0.3 // indirect
	github.com/stretchr/objx v0.5.2 // indirect
	github.com/tiendc/go-deepcopy v1.7.1 // indirect
	github.com/trivago/tgo v1.0.7 // indirect
	github.com/xuri/efp v0.0.1 // indirect
	github.com/xuri/nfp v0.0.2-0.20250530014748-2ddeb826f9a9 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.42.0 // indirect
	go.opentelemetry.io/proto/otlp v1.9.0 // indirect
	go.yaml.in/yaml/v2 v2.4.3 // indirect
	golang.org/x/mod v0.32.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/time v0.14.0 // indirect
	golang.org/x/tools v0.41.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260209200024-4cfbd4190f57 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260209200024-4cfbd4190f57 // indirect
)

require (
	github.com/ClickHouse/ch-go v0.65.0 // indirect
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/bytedance/sonic v1.11.9 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.2-0.20180830191138-d8f796af33cc // indirect
	github.com/dennwc/varint v1.0.0 // indirect
	github.com/felixge/httpsnoop v1.0.4 // indirect
	github.com/fsnotify/fsnotify v1.7.0 // indirect
	github.com/gabriel-vasile/mimetype v1.4.12 // indirect
	github.com/gin-contrib/sse v0.1.0 // indirect
	github.com/go-faster/city v1.0.1 // indirect
	github.com/go-faster/errors v0.7.1 // indirect
	github.com/go-kit/log v0.2.1 // indirect
	github.com/go-logfmt/logfmt v0.5.1 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/goccy/go-json v0.10.3 // indirect
	github.com/grafana/regexp v0.0.0-20220304095617-2e8d9baf4ac2 // indirect
	github.com/hashicorp/hcl v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12
	github.com/klauspost/compress v1.18.3 // indirect
	github.com/klauspost/cpuid/v2 v2.2.8 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/magiconair/properties v1.8.10 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/paulmach/orb v0.12.0 // indirect
	github.com/pelletier/go-toml/v2 v2.2.2 // indirect
	github.com/pierrec/lz4/v4 v4.1.25 // indirect
	github.com/pmezard/go-difflib v1.0.1-0.20181226105442-5d4384ee4fb2 // indirect
	github.com/prometheus/client_golang v1.23.2 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.67.4 // indirect
	github.com/prometheus/procfs v0.19.2 // indirect
	github.com/rabbitmq/amqp091-go v1.10.0 // indirect
	github.com/sagikazarmark/locafero v0.4.0 // indirect
	github.com/sagikazarmark/slog-shim v0.1.0 // indirect
	github.com/samber/slog-multi v1.8.0 // indirect
	github.com/segmentio/asm v1.2.1 // indirect
	github.com/shopspring/decimal v1.4.0 // indirect
	github.com/sourcegraph/conc v0.3.0 // indirect
	github.com/spf13/afero v1.11.0 // indirect
	github.com/spf13/cast v1.6.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/subosito/gotenv v1.6.0 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.2.12 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/goleak v1.3.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/arch v0.8.0 // indirect
	golang.org/x/crypto v0.48.0
	golang.org/x/exp v0.0.0-20251209150349-8475f28825e9 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/ini.v1 v1.67.0 // indirect
)
