package providers

import (
	"encoding/json"
	"strings"
	"testing"

	"cloud.google.com/go/compute/apiv1/computepb"
	"cloud.google.com/go/container/apiv1/containerpb"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/aws/aws-sdk-go-v2/aws"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
	dxtypes "github.com/aws/aws-sdk-go-v2/service/directconnect/types"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const secret = "s3cr3t-value-that-must-not-be-stored"

// asStored converts an SDK object the way the collectors do (structToMap is a
// JSON round trip in every provider), so the tests exercise the real key names.
func asStored(t *testing.T, v any) map[string]any {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func assertNoSecret(t *testing.T, meta map[string]any) {
	t.Helper()
	b, err := json.Marshal(meta)
	require.NoError(t, err)
	assert.NotContains(t, string(b), secret)
}

// assertIdempotent checks that redacting already-redacted meta changes nothing:
// the Azure realtime path feeds stored meta back through the function.
func assertIdempotent(t *testing.T, meta map[string]any) {
	t.Helper()
	once, err := json.Marshal(meta)
	require.NoError(t, err)
	RedactMetaSecrets(meta)
	twice, err := json.Marshal(meta)
	require.NoError(t, err)
	assert.JSONEq(t, string(once), string(twice))
}

func TestRedactsSecretValuesFromProviderObjects(t *testing.T) {
	cases := map[string]any{
		"aws lambda env": lambdatypes.FunctionConfiguration{
			FunctionName: aws.String("fn"),
			Environment:  &lambdatypes.EnvironmentResponse{Variables: map[string]string{"DB_PASSWORD": secret, "STRIPE": secret}},
		},
		"aws ecs task overrides": ecstypes.Task{Overrides: &ecstypes.TaskOverride{ContainerOverrides: []ecstypes.ContainerOverride{{
			Environment: []ecstypes.KeyValuePair{{Name: aws.String("API_CFG"), Value: aws.String(secret)}},
		}}}},
		"aws ecs task definition (event evidence)": ecstypes.TaskDefinition{ContainerDefinitions: []ecstypes.ContainerDefinition{{
			Environment: []ecstypes.KeyValuePair{{Name: aws.String("DB_PASSWORD"), Value: aws.String(secret)}},
		}}},
		"aws cloudfront origin header": cftypes.Distribution{DistributionConfig: &cftypes.DistributionConfig{Origins: &cftypes.Origins{Items: []cftypes.Origin{{
			CustomHeaders: &cftypes.CustomHeaders{Items: []cftypes.OriginCustomHeader{{HeaderName: aws.String("X-Origin-Verify"), HeaderValue: aws.String(secret)}}},
		}}}}},
		"aws direct connect bgp keys": dxtypes.VirtualInterface{
			AuthKey:  aws.String(secret),
			BgpPeers: []dxtypes.BGPPeer{{AuthKey: aws.String(secret)}},
		},
		"azure container app env": armappcontainers.ContainerApp{Properties: &armappcontainers.ContainerAppProperties{Template: &armappcontainers.Template{
			Containers: []*armappcontainers.Container{{Env: []*armappcontainers.EnvironmentVar{{Name: to.Ptr("CONN"), Value: to.Ptr(secret)}}}},
		}}},
		"azure expressroute keys": armnetwork.ExpressRouteCircuit{Properties: &armnetwork.ExpressRouteCircuitPropertiesFormat{
			ServiceKey:     to.Ptr(secret),
			Peerings:       []*armnetwork.ExpressRouteCircuitPeering{{Properties: &armnetwork.ExpressRouteCircuitPeeringPropertiesFormat{SharedKey: to.Ptr(secret)}}},
			Authorizations: []*armnetwork.ExpressRouteCircuitAuthorization{{Properties: &armnetwork.AuthorizationPropertiesFormat{AuthorizationKey: to.Ptr(secret)}}},
		}},
		"azure action group receiver urls": armmonitor.ActionGroup{
			WebhookReceivers:       []*armmonitor.WebhookReceiver{{ServiceURI: to.Ptr("https://hooks.example.com/notify?token=" + secret)}},
			AzureFunctionReceivers: []*armmonitor.AzureFunctionReceiver{{HTTPTriggerURL: to.Ptr("https://fn.azurewebsites.net/api/alert?code=" + secret)}},
			LogicAppReceivers:      []*armmonitor.LogicAppReceiver{{CallbackURL: to.Ptr("https://prod.logic.azure.com/workflows/1/triggers/manual/run?sig=" + secret)}},
		},
		"gcp compute startup script": &computepb.Instance{Metadata: &computepb.Metadata{Items: []*computepb.Items{
			{Key: to.Ptr("startup-script"), Value: to.Ptr("export TOKEN=" + secret)},
		}}},
		// MasterAuth.Password and Cluster.NodeConfig are deprecated, but legacy
		// clusters still return them, which is why they are redacted.
		"gcp gke auth and node metadata": &containerpb.Cluster{
			MasterAuth: &containerpb.MasterAuth{Password: secret, ClientKey: secret},             //nolint:staticcheck // legacy field still returned
			NodeConfig: &containerpb.NodeConfig{Metadata: map[string]string{"kube-env": secret}}, //nolint:staticcheck // legacy field still returned
			NodePools:  []*containerpb.NodePool{{Config: &containerpb.NodeConfig{Metadata: map[string]string{"startup": secret}}}},
		},
	}
	for name, obj := range cases {
		t.Run(name, func(t *testing.T) {
			meta := asStored(t, obj)
			b, _ := json.Marshal(meta)
			require.Contains(t, string(b), secret, "fixture must carry the secret before redaction")

			RedactMetaSecrets(meta)

			assertNoSecret(t, meta)
			assertIdempotent(t, meta)
		})
	}
}

func TestKeepsWhatIsNotASecret(t *testing.T) {
	t.Run("lambda env references and names", func(t *testing.T) {
		vars := map[string]string{
			"QUEUE_URL":      "https://sqs.us-east-1.amazonaws.com/123456789012/orders",
			"FIFO_QUEUE":     "https://sqs.eu-west-1.amazonaws.com/123456789012/orders.fifo",
			"LEGACY_QUEUE":   "https://queue.amazonaws.com/123456789012/legacy",
			"REGIONAL_QUEUE": "https://us-west-2.queue.amazonaws.com/123456789012/regional",
			"CN_QUEUE":       "https://sqs.cn-north-1.amazonaws.com.cn/123456789012/cn",
			"DUALSTACK":      "https://sqs.us-east-1.api.aws/123456789012/dual",
			"ISOF_TOPIC":     "arn:aws-iso-f:sns:us-isof-south-1:123456789012:t",
			"VPCE_QUEUE":     "https://vpce-0abc123-def456.sqs.us-east-1.vpce.amazonaws.com/123456789012/private",
			"TOPIC_ARN":      "arn:aws:sns:us-east-1:123456789012:alerts",
			"GOV_BUCKET":     "arn:aws-us-gov:s3:::records",
			"CN_TABLE_ARN":   "arn:aws-cn:dynamodb:cn-north-1:123456789012:table/t",
			"DYNAMODB_TABLE": "orders-table",
			"DB_PASSWORD":    secret,
			"CREDS_URL":      "https://user:" + secret + "@db.example.com/x",
		}
		meta := asStored(t, lambdatypes.FunctionConfiguration{
			KMSKeyArn:   aws.String("arn:aws:kms:us-east-1:123456789012:key/k"),
			Environment: &lambdatypes.EnvironmentResponse{Variables: vars},
		})

		RedactMetaSecrets(meta)

		got := meta["Environment"].(map[string]any)["Variables"].(map[string]any)
		for k, v := range vars {
			if k == "DB_PASSWORD" || k == "CREDS_URL" {
				assert.Equal(t, RedactedValue, got[k], k)
				continue
			}
			assert.Equal(t, v, got[k], "%s is a resource reference and must be kept", k)
		}
		assert.Equal(t, "arn:aws:kms:us-east-1:123456789012:key/k", meta["KMSKeyArn"])
	})

	t.Run("fields that name a secret without holding it", func(t *testing.T) {
		meta := map[string]any{
			"SecretArn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:db",
			"NextToken": "page-2",
			"KmsKeyId":  "key-id",
			"MasterUserSecret": map[string]any{
				"SecretArn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:rds",
			},
		}
		before, _ := json.Marshal(meta)
		RedactMetaSecrets(meta)
		after, _ := json.Marshal(meta)
		assert.JSONEq(t, string(before), string(after))
	})

	t.Run("receiver url keeps scheme host and path", func(t *testing.T) {
		meta := map[string]any{"webhookReceivers": []any{map[string]any{"serviceUri": "https://hooks.example.com/notify?token=" + secret}}}
		RedactMetaSecrets(meta)
		assert.Equal(t, "https://hooks.example.com/notify", meta["webhookReceivers"].([]any)[0].(map[string]any)["serviceUri"])
	})
}

// Collectors sometimes build Meta by hand with typed containers (Direct Connect
// stores VirtualInterfaces as []map[string]any); those must be walked too.
func TestRedactsHandBuiltTypedContainers(t *testing.T) {
	meta := map[string]any{
		"VirtualInterfaces": []map[string]any{{"AuthKey": secret, "BgpPeers": []any{map[string]any{"AuthKey": secret}}}},
		"Environment":       map[string]any{"Variables": map[string]string{"API_KEY": secret}},
		"Settings":          map[string]string{"password": secret},
	}
	RedactMetaSecrets(meta)
	assertNoSecret(t, meta)
}

func TestRedactMetaJSON(t *testing.T) {
	in := `{"Environment":{"Variables":{"DB_PASSWORD":"` + secret + `"}},"nb_source":"api"}`
	out := RedactMetaJSON(in)
	assert.False(t, strings.Contains(out, secret))
	assert.Contains(t, out, `"nb_source":"api"`)
	assert.Equal(t, out, RedactMetaJSON(out), "idempotent")
	assert.Equal(t, "not json", RedactMetaJSON("not json"))
	assert.Contains(t, RedactMetaJSON(`{"Size":9007199254740993}`), `9007199254740993`, "integers above 2^53 stay exact")
}
