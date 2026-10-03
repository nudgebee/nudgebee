package aws

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	// cliCredentialsMinDuration is the shortest session STS accepts.
	cliCredentialsMinDuration = 15 * time.Minute
	// cliCredentialsMaxDuration bounds how long a credential handed to
	// customer-authored code stays usable. An hour is also the default ceiling
	// on a role that has not raised MaxSessionDuration, so asking for more
	// would fail on most accounts anyway.
	cliCredentialsMaxDuration = time.Hour
)

// cliSessionPolicy narrows an assumed-role session below what the role itself
// allows. It is an allow-everything policy with two carve-outs:
//
//   - role assumption, so a session cannot be traded for a different role's
//     access. sts:GetCallerIdentity stays allowed: the AWS provider in
//     terraform calls it on startup, and scripts use it to prove which account
//     they are in.
//   - the IAM calls that mint standing credentials, so an hour-long session
//     cannot be turned into permanent access.
//
// Everything ordinary infrastructure work needs — including creating roles and
// instance profiles and passing them to instances — is untouched.
func cliSessionPolicy() (string, error) {
	policy := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect":   "Allow",
				"Action":   "*",
				"Resource": "*",
			},
			{
				"Effect": "Deny",
				"Action": []string{
					"sts:AssumeRole",
					"sts:AssumeRoleWithSAML",
					"sts:AssumeRoleWithWebIdentity",
					"sts:GetFederationToken",
					"iam:CreateAccessKey",
					"iam:UpdateAccessKey",
					"iam:CreateUser",
					"iam:CreateLoginProfile",
					"iam:UpdateLoginProfile",
					"iam:AttachUserPolicy",
					"iam:PutUserPolicy",
				},
				"Resource": "*",
			},
		},
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return "", fmt.Errorf("aws: failed to build session policy: %w", err)
	}
	return string(encoded), nil
}

// clampCliCredentialsDuration keeps a requested session inside what STS accepts
// and what we are willing to hand out.
func clampCliCredentialsDuration(requested time.Duration) time.Duration {
	if requested <= 0 || requested > cliCredentialsMaxDuration {
		return cliCredentialsMaxDuration
	}
	if requested < cliCredentialsMinDuration {
		return cliCredentialsMinDuration
	}
	return requested
}

// CliCredentials returns an environment that authenticates the AWS CLI, the
// SDKs and terraform as this account.
//
// Role-connected accounts get a scoped session that expires. Key-connected
// accounts get the stored key, because there is nothing better to give them:
// sts:GetSessionToken cannot carry a policy and cannot call IAM, so a terraform
// run that creates a role would fail half-way through rather than up front. The
// caller is told which kind it received through ExpiresAt.
func (a *awsProvider) CliCredentials(ctx providers.CloudProviderContext, account providers.Account, request providers.CliCredentialsRequest) (providers.CliCredentials, error) {
	region := "us-east-1"
	if account.Region != nil && *account.Region != "" {
		region = *account.Region
	}
	env := map[string]string{
		"AWS_REGION":         region,
		"AWS_DEFAULT_REGION": region,
	}

	if account.AssumeRole != nil && strings.TrimSpace(*account.AssumeRole) != "" {
		policy, err := cliSessionPolicy()
		if err != nil {
			return providers.CliCredentials{}, err
		}
		baseCfg, err := config.LoadDefaultConfig(ctx.GetContext(), config.WithRegion(region))
		if err != nil {
			return providers.CliCredentials{}, fmt.Errorf("aws: failed to load base config: %w", err)
		}
		input := &sts.AssumeRoleInput{
			RoleArn:         account.AssumeRole,
			RoleSessionName: aws.String("nudgebee-workflow-script"),
			DurationSeconds: aws.Int32(int32(clampCliCredentialsDuration(request.Duration).Seconds())),
			Policy:          aws.String(policy),
		}
		// A trust policy carrying an sts:ExternalId condition rejects a call
		// that omits it, so send the stored value the way every other caller
		// here does.
		if account.ExternalId != nil {
			if extID := strings.TrimSpace(*account.ExternalId); extID != "" {
				input.ExternalId = aws.String(extID)
			}
		}
		out, err := sts.NewFromConfig(baseCfg).AssumeRole(ctx.GetContext(), input)
		if err != nil {
			return providers.CliCredentials{}, fmt.Errorf("aws: assume role %s: %w", *account.AssumeRole, err)
		}
		if out == nil || out.Credentials == nil {
			return providers.CliCredentials{}, fmt.Errorf("aws: assume role %s returned no credentials", *account.AssumeRole)
		}
		env["AWS_ACCESS_KEY_ID"] = aws.ToString(out.Credentials.AccessKeyId)
		env["AWS_SECRET_ACCESS_KEY"] = aws.ToString(out.Credentials.SecretAccessKey)
		env["AWS_SESSION_TOKEN"] = aws.ToString(out.Credentials.SessionToken)
		return providers.CliCredentials{Env: env, ExpiresAt: out.Credentials.Expiration}, nil
	}

	if account.AccessKey != nil && account.AccessSecret != nil && *account.AccessSecret != "" {
		secret, err := common.Decrypt(*account.AccessSecret)
		if err != nil {
			return providers.CliCredentials{}, fmt.Errorf("aws: failed to decrypt stored credentials: %w", err)
		}
		env["AWS_ACCESS_KEY_ID"] = *account.AccessKey
		env["AWS_SECRET_ACCESS_KEY"] = secret
		return providers.CliCredentials{Env: env}, nil
	}

	return providers.CliCredentials{}, fmt.Errorf("aws: account has no role or access key configured")
}
