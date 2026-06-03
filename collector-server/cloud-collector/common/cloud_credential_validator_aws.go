package common

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/costandusagereportservice"
	curtypes "github.com/aws/aws-sdk-go-v2/service/costandusagereportservice/types"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

const (
	CloudProviderAWS CloudProvider = "AWS"

	// AWS permissions
	PermissionAWSGetCallerIdentity PermissionType = "STS GetCallerIdentity"
	PermissionAWSCURDescribe       PermissionType = "Cost & Usage Report (CUR) Discovery"
	PermissionAWSCURS3Access       PermissionType = "CUR S3 Bucket Access"

	// CurFormatTextOrCSV / CurTimeUnitDaily are the only CUR variants the collector
	// can ingest today (see usage_report.go:235-241). The validator MUST keep this
	// filter in sync.
	CurFormatTextOrCSV = "textORcsv"
	CurTimeUnitDaily   = "DAILY"
)

// AWSCredentials carries either an IAM role ARN (cross-account) or static
// access keys. Exactly one mode must be populated.
type AWSCredentials struct {
	// AssumeRole: ARN of the role to assume. When set, AccessKey/AccessSecret
	// must be empty.
	AssumeRole string
	// ExternalId is optional — only used when AssumeRole is set.
	ExternalId string

	// AccessKey + AccessSecret: static IAM user credentials. When set,
	// AssumeRole must be empty. AccessSecret is plaintext here (not the
	// encrypted form stored in cloud_accounts.access_secret).
	AccessKey    string
	AccessSecret string

	// Region used for SDK config. Defaults to us-east-1 when empty.
	Region string
}

// AWSCurInfo describes a discovered CUR report. Mirrors the JSON shape
// already persisted by the CF callback into cloud_accounts.data
// (event_org_registration.go:676-686) so the access-keys flow can populate
// the same columns.
type AWSCurInfo struct {
	BucketName  string `json:"bucketName"`
	Region      string `json:"region"`
	Prefix      string `json:"prefix"`
	ReportName  string `json:"reportName"`
	Compression string `json:"compression"`
	TimeUnit    string `json:"timeUnit"`
	Versioning  string `json:"versioning"`
	Format      string `json:"format"`
}

// ValidateAWSCredentials checks that the supplied credentials can:
//  1. Call sts:GetCallerIdentity
//  2. Enumerate a usable CUR report (textORcsv + DAILY) via cur:DescribeReportDefinitions
//  3. Read from the CUR S3 bucket (GetBucketLocation + ListObjectsV2 with the report prefix)
//
// All three steps are required for success (hard block on onboarding —
// see plan). On the first failure we stop and surface a precise error
// message identifying the failing step.
func ValidateAWSCredentials(ctx context.Context, creds AWSCredentials) ValidationResult {
	result := ValidationResult{
		Success:            true,
		Provider:           CloudProviderAWS,
		PermissionDetails:  []PermissionStatus{},
		MissingPermissions: []PermissionType{},
	}

	if err := awsCredsBasicCheck(creds); err != nil {
		result.Success = false
		result.ErrorMessage = err.Error()
		return result
	}

	cfg, err := buildAWSConfigForValidation(ctx, creds)
	if err != nil {
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("failed to build AWS config: %v", err)
		return result
	}

	// Step 1: STS GetCallerIdentity
	stsStatus := PermissionStatus{Permission: PermissionAWSGetCallerIdentity}
	accountNumber, stsErr := checkAWSCallerIdentity(ctx, cfg)
	if stsErr != nil {
		stsStatus.HasAccess = false
		stsStatus.ErrorDetail = stsErr.Error()
		result.PermissionDetails = append(result.PermissionDetails, stsStatus)
		result.MissingPermissions = append(result.MissingPermissions, PermissionAWSGetCallerIdentity)
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("AWS authentication failed: %s", stsErr.Error())
		return result
	}
	stsStatus.HasAccess = true
	result.PermissionDetails = append(result.PermissionDetails, stsStatus)
	result.AccountNumber = accountNumber

	// Step 2: CUR DescribeReportDefinitions
	curStatus := PermissionStatus{Permission: PermissionAWSCURDescribe}
	report, curErr := discoverAWSCURReport(ctx, cfg)
	if curErr != nil {
		curStatus.HasAccess = false
		curStatus.ErrorDetail = curErr.Error()
		result.PermissionDetails = append(result.PermissionDetails, curStatus)
		result.MissingPermissions = append(result.MissingPermissions, PermissionAWSCURDescribe)
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("Cost & Usage Report discovery failed: %s", curErr.Error())
		return result
	}
	curStatus.HasAccess = true
	result.PermissionDetails = append(result.PermissionDetails, curStatus)
	result.Cur = report

	// Step 3: CUR S3 access — verify we can actually read from the bucket.
	s3Status := PermissionStatus{Permission: PermissionAWSCURS3Access}
	if err := checkAWSCURS3Access(ctx, cfg, report); err != nil {
		s3Status.HasAccess = false
		s3Status.ErrorDetail = err.Error()
		result.PermissionDetails = append(result.PermissionDetails, s3Status)
		result.MissingPermissions = append(result.MissingPermissions, PermissionAWSCURS3Access)
		result.Success = false
		result.ErrorMessage = fmt.Sprintf("CUR S3 bucket access check failed: %s", err.Error())
		return result
	}
	s3Status.HasAccess = true
	result.PermissionDetails = append(result.PermissionDetails, s3Status)

	return result
}

func awsCredsBasicCheck(c AWSCredentials) error {
	hasRole := strings.TrimSpace(c.AssumeRole) != ""
	hasKeys := strings.TrimSpace(c.AccessKey) != "" && strings.TrimSpace(c.AccessSecret) != ""
	if !hasRole && !hasKeys {
		return errors.New("either assume_role or access_key+access_secret is required")
	}
	if hasRole && hasKeys {
		return errors.New("provide either assume_role or access_key+access_secret, not both")
	}
	return nil
}

// buildAWSConfigForValidation builds an aws.Config from the user-provided
// credentials. It deliberately does NOT decrypt anything (the validator
// receives plaintext from the onboarding wizard), and does NOT consult the
// AWS_PROFILE / instance-profile chain — validation must reflect the exact
// credentials the user typed in.
func buildAWSConfigForValidation(ctx context.Context, c AWSCredentials) (aws.Config, error) {
	region := c.Region
	if strings.TrimSpace(region) == "" {
		region = "us-east-1"
	}

	if strings.TrimSpace(c.AccessKey) != "" {
		staticCreds := credentials.NewStaticCredentialsProvider(c.AccessKey, c.AccessSecret, "")
		return config.LoadDefaultConfig(ctx,
			config.WithRegion(region),
			config.WithCredentialsProvider(staticCreds),
			// Don't read shared config / env vars during validation — we want
			// to validate exactly what the user typed.
			config.WithSharedConfigFiles(nil),
			config.WithSharedCredentialsFiles(nil),
		)
	}

	// Assume-role path: SDK needs a base config to call sts:AssumeRole.
	// Use the default chain so the collector's own IAM identity is the
	// caller (matches getAwsConfigFromAccount's existing behavior).
	baseCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return aws.Config{}, err
	}
	stsClient := sts.NewFromConfig(baseCfg)
	provider := stscreds.NewAssumeRoleProvider(stsClient, c.AssumeRole, func(o *stscreds.AssumeRoleOptions) {
		o.RoleSessionName = "nudgebee-onboarding-validate"
		if strings.TrimSpace(c.ExternalId) != "" {
			o.ExternalID = aws.String(c.ExternalId)
		}
	})
	baseCfg.Credentials = aws.NewCredentialsCache(provider)
	return baseCfg, nil
}

func checkAWSCallerIdentity(ctx context.Context, cfg aws.Config) (string, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	stsClient := sts.NewFromConfig(cfg)
	out, err := stsClient.GetCallerIdentity(queryCtx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", err
	}
	return aws.ToString(out.Account), nil
}

// discoverAWSCURReport mirrors the filter in usage_report.go:235-241 — the
// collector only ingests DAILY + textORcsv reports, so an account with only
// hourly/parquet CURs is unusable today. Auto-pick the first match
// (decision: "Auto-pick first DAILY+textORcsv"), but skip reports whose
// AWS-reported status indicates the S3 bucket is gone or CUR delivery
// permissions are broken — picking those would surface as a confusing
// NoSuchBucket / AccessDenied during the downstream S3 probe.
func discoverAWSCURReport(ctx context.Context, cfg aws.Config) (*AWSCurInfo, error) {
	queryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// CUR API is hosted only in us-east-1.
	curCfg := cfg.Copy()
	curCfg.Region = "us-east-1"
	svc := costandusagereportservice.NewFromConfig(curCfg)

	var nextToken *string
	var skippedUnhealthy int
	for {
		out, err := svc.DescribeReportDefinitions(queryCtx, &costandusagereportservice.DescribeReportDefinitionsInput{
			NextToken: nextToken,
		})
		if err != nil {
			return nil, err
		}

		for _, r := range out.ReportDefinitions {
			if !curMatchesIngestionFilter(r) {
				continue
			}
			if !curHasUsableStatus(r) {
				skippedUnhealthy++
				continue
			}
			info := &AWSCurInfo{
				BucketName:  aws.ToString(r.S3Bucket),
				Region:      string(r.S3Region),
				Prefix:      aws.ToString(r.S3Prefix) + "/" + aws.ToString(r.ReportName),
				ReportName:  aws.ToString(r.ReportName),
				Compression: string(r.Compression),
				TimeUnit:    string(r.TimeUnit),
				Versioning:  string(r.ReportVersioning),
				Format:      string(r.Format),
			}
			return info, nil
		}

		nextToken = out.NextToken
		if nextToken == nil {
			break
		}
	}

	if skippedUnhealthy > 0 {
		return nil, fmt.Errorf("found %d Cost & Usage Report(s) matching the required format (DAILY + textORcsv) "+
			"but all were in an error state (ERROR_NO_BUCKET / ERROR_PERMISSIONS). "+
			"Fix or delete the broken reports in the AWS billing console, then retry", skippedUnhealthy)
	}
	return nil, errors.New("no Cost & Usage Report found matching the required format (DAILY + textORcsv). " +
		"Configure a CUR report in the AWS billing console with TimeUnit=DAILY and Format=text/csv, then retry")
}

// curMatchesIngestionFilter encodes the filter applied by
// getUsageBucketFromCostReport (usage_report.go:235-241). Keep both call
// sites in sync — see plan "Filter drift risk".
func curMatchesIngestionFilter(r curtypes.ReportDefinition) bool {
	if string(r.Format) != CurFormatTextOrCSV {
		return false
	}
	if string(r.TimeUnit) != CurTimeUnitDaily {
		return false
	}
	return true
}

// curHasUsableStatus returns false when AWS already knows the CUR cannot
// be delivered — the S3 bucket is missing or CUR has lost permission to
// write to it. Picking such a report would surface as a confusing
// NoSuchBucket / AccessDenied during the S3 probe later in validation.
// Reports with nil/empty status (brand-new CURs that haven't delivered
// once yet) are treated as usable so first-time onboards aren't blocked.
func curHasUsableStatus(r curtypes.ReportDefinition) bool {
	if r.ReportStatus == nil {
		return true
	}
	switch r.ReportStatus.LastStatus {
	case curtypes.LastStatusErrorNoBucket, curtypes.LastStatusErrorPermissions:
		return false
	}
	return true
}

func checkAWSCURS3Access(ctx context.Context, cfg aws.Config, report *AWSCurInfo) error {
	if report == nil || report.BucketName == "" {
		return errors.New("no CUR bucket to verify")
	}

	queryCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Target the bucket's home region — S3 requests routed to the wrong
	// region surface as 301 PermanentRedirect, not an auth failure.
	s3Cfg := cfg.Copy()
	if strings.TrimSpace(report.Region) != "" {
		s3Cfg.Region = report.Region
	}

	s3Client := s3.NewFromConfig(s3Cfg, func(o *s3.Options) {
		o.UsePathStyle = false
	})

	// GetBucketLocation is a cheap permission probe that fails fast on
	// missing s3:GetBucketLocation.
	if _, err := s3Client.GetBucketLocation(queryCtx, &s3.GetBucketLocationInput{
		Bucket: aws.String(report.BucketName),
	}); err != nil {
		return fmt.Errorf("s3:GetBucketLocation on %q failed: %w", report.BucketName, err)
	}

	// ListObjectsV2 with the report prefix proves the role/keys can
	// actually read CUR objects — this is the permission the collector
	// needs at sync time.
	prefix := report.Prefix
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	maxKeys := int32(1)
	if _, err := s3Client.ListObjectsV2(queryCtx, &s3.ListObjectsV2Input{
		Bucket:  aws.String(report.BucketName),
		Prefix:  aws.String(prefix),
		MaxKeys: aws.Int32(maxKeys),
	}); err != nil {
		return fmt.Errorf("s3:ListBucket on %q (prefix %q) failed: %w", report.BucketName, prefix, err)
	}

	return nil
}
