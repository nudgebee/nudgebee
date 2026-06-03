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
