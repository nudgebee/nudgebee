package core

import (
	"context"
	"log/slog"
	"sync"
	"time"

	awsSDK "github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
)

// ambientAWSConfigs caches the loaded aws.Config per region so the credential
// chain (env, shared config, IRSA, IMDS) is walked once rather than on every
// forwarded request. LoadDefaultConfig wraps the resolved provider in an
// aws.CredentialsCache, so Retrieve serves from that cache and refreshes
// temporary credentials on expiry by itself.
var (
	ambientAWSConfigMu sync.Mutex
	ambientAWSConfigs  = map[string]awsSDK.Config{}
)

func ambientAWSConfigFor(region string) (awsSDK.Config, error) {
	ambientAWSConfigMu.Lock()
	cfg, ok := ambientAWSConfigs[region]
	ambientAWSConfigMu.Unlock()
	if ok {
		return cfg, nil
	}

	// Loaded outside the lock: resolving the chain can reach IMDS or STS, and
	// holding the mutex across that would stall every other caller — including
	// cache hits for unrelated regions — for up to the timeout below. Two
	// goroutines racing on the same cold region may both load; that is benign
	// (the configs are equivalent and the loser is simply overwritten) and far
	// cheaper than serializing all callers behind one network round-trip.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	opts := []func(*awsConfig.LoadOptions) error{}
	if region != "" {
		opts = append(opts, awsConfig.WithRegion(region))
	}
	loaded, err := awsConfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return awsSDK.Config{}, err
	}

	ambientAWSConfigMu.Lock()
	ambientAWSConfigs[region] = loaded
	ambientAWSConfigMu.Unlock()
	return loaded, nil
}

// ambientBedrockCredentials returns the AWS credentials llm-server itself
// authenticates Bedrock with, so the code-analysis sandbox can run on the same
// identity instead of needing its own configuration.
//
// llm-server resolves Bedrock through the AWS default chain — logged as
// credSource="default-chain" — which in practice finds the
// AWS_ACCESS_KEY_ID/AWS_SECRET_ACCESS_KEY it receives from the cluster secret,
// or a node role / IRSA. The workspace pod running code analysis is handed only
// LLM_* keys by design (see workspace.LLMSecretEnvVars, which deliberately
// withholds cloud credentials from the pod that executes model-generated
// commands) and therefore has no AWS environment of its own. Walking the same
// chain there dead-ends at IMDS, which exists on EKS but not on GKE.
//
// Returns empty strings when nothing resolves; the caller then forwards no
// credentials and the pod falls back to its own chain, which is the
// pre-existing behavior. The returned values are live credentials and MUST NOT
// be logged.
func ambientBedrockCredentials(region string) (string, string, string) {
	cfg, err := ambientAWSConfigFor(region)
	if err != nil {
		slog.Warn("forwarding: could not load AWS config for ambient Bedrock credentials", "error", err, "region", region)
		return "", "", ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		slog.Warn("forwarding: could not retrieve ambient Bedrock credentials", "error", err, "region", region)
		return "", "", ""
	}
	// Both halves are required: the AWS SDK rejects a half-set static provider
	// outright rather than falling through to the next source, so forwarding one
	// without the other would break the pod's own chain instead of helping it.
	if creds.AccessKeyID == "" || creds.SecretAccessKey == "" {
		return "", "", ""
	}
	return creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken
}
