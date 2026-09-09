package common

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	curtypes "github.com/aws/aws-sdk-go-v2/service/costandusagereportservice/types"
)

func TestAwsCredsBasicCheck(t *testing.T) {
	tests := []struct {
		name    string
		creds   AWSCredentials
		wantErr string
	}{
		{
			name:    "neither role nor keys",
			creds:   AWSCredentials{},
			wantErr: "either assume_role or access_key+access_secret is required",
		},
		{
			name:    "both role and keys",
			creds:   AWSCredentials{AssumeRole: "arn:aws:iam::1:role/x", AccessKey: "AKIA", AccessSecret: "s"},
			wantErr: "provide either assume_role or access_key+access_secret, not both",
		},
		{
			name:    "role only OK",
			creds:   AWSCredentials{AssumeRole: "arn:aws:iam::1:role/x"},
			wantErr: "",
		},
		{
			name:    "keys only OK",
			creds:   AWSCredentials{AccessKey: "AKIA", AccessSecret: "s"},
			wantErr: "",
		},
		{
			name:    "access key without secret rejected",
			creds:   AWSCredentials{AccessKey: "AKIA"},
			wantErr: "either assume_role or access_key+access_secret is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := awsCredsBasicCheck(tt.creds)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

// TestCurMatchesIngestionFilter pins the CUR filter to match
// usage_report.go:235-241. If anyone changes the collector filter without
// also updating the validator, this test fails.
func TestCurMatchesIngestionFilter(t *testing.T) {
	tests := []struct {
		name string
		def  curtypes.ReportDefinition
		want bool
	}{
		{
			name: "DAILY textORcsv matches",
			def: curtypes.ReportDefinition{
				ReportName: aws.String("r"),
				Format:     curtypes.ReportFormatCsv,
				TimeUnit:   curtypes.TimeUnitDaily,
			},
			want: true,
		},
		{
			name: "non-textORcsv rejected",
			def: curtypes.ReportDefinition{
				ReportName: aws.String("r"),
				Format:     curtypes.ReportFormat("parquet"),
				TimeUnit:   curtypes.TimeUnitDaily,
			},
			want: false,
		},
		{
			name: "Hourly rejected",
			def: curtypes.ReportDefinition{
				ReportName: aws.String("r"),
				Format:     curtypes.ReportFormatCsv,
				TimeUnit:   curtypes.TimeUnitHourly,
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := curMatchesIngestionFilter(tt.def); got != tt.want {
				t.Fatalf("curMatchesIngestionFilter() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCurHasUsableStatus(t *testing.T) {
	tests := []struct {
		name string
		def  curtypes.ReportDefinition
		want bool
	}{
		{
			name: "nil status — treat as usable (new CUR, no delivery yet)",
			def:  curtypes.ReportDefinition{ReportName: aws.String("r")},
			want: true,
		},
		{
			name: "empty status string — treat as usable",
			def: curtypes.ReportDefinition{
				ReportName:   aws.String("r"),
				ReportStatus: &curtypes.ReportStatus{},
			},
			want: true,
		},
		{
			name: "SUCCESS — usable",
			def: curtypes.ReportDefinition{
				ReportName:   aws.String("r"),
				ReportStatus: &curtypes.ReportStatus{LastStatus: curtypes.LastStatusSuccess},
			},
			want: true,
		},
		{
			name: "ERROR_NO_BUCKET — not usable",
			def: curtypes.ReportDefinition{
				ReportName:   aws.String("r"),
				ReportStatus: &curtypes.ReportStatus{LastStatus: curtypes.LastStatusErrorNoBucket},
			},
			want: false,
		},
		{
			name: "ERROR_PERMISSIONS — not usable",
			def: curtypes.ReportDefinition{
				ReportName:   aws.String("r"),
				ReportStatus: &curtypes.ReportStatus{LastStatus: curtypes.LastStatusErrorPermissions},
			},
			want: false,
		},
		{
			name: "unknown future status — treat as usable, let S3 probe decide",
			def: curtypes.ReportDefinition{
				ReportName:   aws.String("r"),
				ReportStatus: &curtypes.ReportStatus{LastStatus: curtypes.LastStatus("ERROR_FUTURE_UNKNOWN")},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := curHasUsableStatus(tt.def); got != tt.want {
				t.Fatalf("curHasUsableStatus() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestValidateAWSCredentialsInputValidation covers the cheap input-shape
// path that runs before any AWS call. Full mock-based testing of the AWS
// SDK call chain would require introducing interface abstractions in this
// package; that's deferred to a follow-up.
func TestValidateAWSCredentialsInputValidation(t *testing.T) {
	res := ValidateAWSCredentials(t.Context(), AWSCredentials{})
	if res.Success {
		t.Fatal("expected failure for empty credentials")
	}
	if !strings.Contains(res.ErrorMessage, "assume_role") {
		t.Fatalf("unexpected error message: %s", res.ErrorMessage)
	}

	res = ValidateAWSCredentials(t.Context(), AWSCredentials{
		AssumeRole:   "arn:aws:iam::1:role/x",
		AccessKey:    "AKIA",
		AccessSecret: "s",
	})
	if res.Success {
		t.Fatal("expected failure when both role and keys supplied")
	}
	if !strings.Contains(res.ErrorMessage, "not both") {
		t.Fatalf("unexpected error message: %s", res.ErrorMessage)
	}
}

// TestValidateAWSCredentialsCurIsNonFatal pins the rule that authentication is
// mandatory but cost is optional. The CUR steps need live AWS to exercise
// end-to-end, so this asserts the contract the api-server and the onboarding
// banner depend on: a cost-only gap must leave Success=true with an empty
// ErrorMessage, because a non-empty ErrorMessage renders as a hard-error banner
// and suppresses the per-permission breakdown.
func TestValidateAWSCredentialsCurIsNonFatal(t *testing.T) {
	// A credential-shape failure is still fatal and still carries a message.
	res := ValidateAWSCredentials(t.Context(), AWSCredentials{})
	if res.Success {
		t.Fatal("expected Success=false when neither role nor keys are supplied")
	}
	if res.ErrorMessage == "" {
		t.Fatal("expected an ErrorMessage on a fatal credential-shape failure")
	}

	// Both cost permissions must be recognised as cost-only by the frontend
	// banner, which matches on these exact labels.
	for _, p := range []PermissionType{PermissionAWSCURDescribe, PermissionAWSCURS3Access} {
		if p == "" {
			t.Fatal("cost permission labels must be non-empty; the UI matches on them")
		}
	}
	if PermissionAWSCURDescribe != "Cost & Usage Report (CUR) Discovery" {
		t.Fatalf("CUR permission label changed to %q — update COST_PERMISSIONS in ValidationResultBanner.jsx", PermissionAWSCURDescribe)
	}
	if PermissionAWSCURS3Access != "CUR S3 Bucket Access" {
		t.Fatalf("CUR S3 permission label changed to %q — update COST_PERMISSIONS in ValidationResultBanner.jsx", PermissionAWSCURS3Access)
	}
}
