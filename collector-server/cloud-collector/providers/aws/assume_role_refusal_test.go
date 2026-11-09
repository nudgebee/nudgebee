package aws

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/collector/cloud/providers"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/smithy-go"
)

const (
	testRoleArn         = "arn:aws:iam::111122223333:role/Example"
	testAssumeRoleReply = "User: arn:aws:iam::123456789012:user/collector is not authorized to perform: sts:AssumeRole on resource: " + testRoleArn
)

// awsErrorServer answers every request with one AWS error response.
func awsErrorServer(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// readCostReportWithRefusedRole returns the error the SDK itself produces when
// a cost report read needs credentials and AWS refuses sts:AssumeRole. It is
// produced by the real client rather than built by hand because the fix depends
// on how the SDK nests that refusal, which an SDK upgrade could change.
func readCostReportWithRefusedRole(t *testing.T) error {
	t.Helper()
	stsURL := awsErrorServer(t, http.StatusForbidden, `<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><Error><Type>Sender</Type><Code>AccessDenied</Code><Message>`+
		testAssumeRoleReply+`</Message></Error><RequestId>example</RequestId></ErrorResponse>`)
	stsClient := sts.New(sts.Options{
		Region:           "us-east-1",
		BaseEndpoint:     aws.String(stsURL),
		Credentials:      credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", ""),
		RetryMaxAttempts: 1,
	})
	s3Client := s3.New(s3.Options{
		Region:           "us-east-1",
		Credentials:      aws.NewCredentialsCache(stscreds.NewAssumeRoleProvider(stsClient, testRoleArn)),
		RetryMaxAttempts: 1,
	})
	_, err := s3Client.GetObject(context.Background(), &s3.GetObjectInput{Bucket: aws.String("cur-bucket"), Key: aws.String("manifest.json")})
	if err == nil {
		t.Fatal("expected the cost report read to fail")
	}
	return fmt.Errorf("aws: failed to fetch cost report manifest for bucket %q key %q: %w", "cur-bucket", "manifest.json", err)
}

// readCostReportWithRefusedBucket returns the SDK's error for a working
// credential whose read of the cost report bucket is denied.
func readCostReportWithRefusedBucket(t *testing.T) error {
	t.Helper()
	s3URL := awsErrorServer(t, http.StatusForbidden, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>AccessDenied</Code><Message>Access Denied</Message><RequestId>example</RequestId><HostId>example</HostId></Error>`)
	s3Client := s3.New(s3.Options{
		Region:           "us-east-1",
		BaseEndpoint:     aws.String(s3URL),
		UsePathStyle:     true,
		Credentials:      credentials.NewStaticCredentialsProvider("AKIDEXAMPLE", "secret", ""),
		RetryMaxAttempts: 1,
	})
	_, err := s3Client.GetObject(context.Background(), &s3.GetObjectInput{Bucket: aws.String("cur-bucket"), Key: aws.String("manifest.json")})
	if err == nil {
		t.Fatal("expected the cost report read to fail")
	}
	return fmt.Errorf("aws: failed to fetch cost report manifest for bucket %q key %q: %w", "cur-bucket", "manifest.json", err)
}

// nestedAssumeRoleError is the SDK's nesting built by hand, for the operations
// and codes that are not worth a fake server each.
func nestedAssumeRoleError(outerService, outerOperation, stsCode string) error {
	stsErr := &smithy.OperationError{
		ServiceID:     "STS",
		OperationName: "AssumeRole",
		Err:           fmt.Errorf("https response error StatusCode: 403, RequestID: example, %w", &smithy.GenericAPIError{Code: stsCode, Message: testAssumeRoleReply}),
	}
	return &smithy.OperationError{
		ServiceID:     outerService,
		OperationName: outerOperation,
		Err:           fmt.Errorf("get identity: %w", fmt.Errorf("get credentials: %w", fmt.Errorf("failed to refresh cached credentials, %w", stsErr))),
	}
}

// TestRefusedRoleIsNotCostNotConfigured pins the two AccessDenied shapes a cost
// report read can fail with, which carry the same error code and mean opposite
// things for the account.
//
// A refused bucket is an account without cost access: supported, and it stays
// connected. A refused role is an account nothing can be collected from. Filing
// the second under the first left such accounts showing Connected for as long
// as the role stayed broken, with only a Resources error to say otherwise.
func TestRefusedRoleIsNotCostNotConfigured(t *testing.T) {
	t.Run("a refused role is a real failure", func(t *testing.T) {
		err := readCostReportWithRefusedRole(t)
		if _, refused := assumeRoleRefusal(err); !refused {
			t.Fatalf("the SDK's error for a refused sts:AssumeRole was not recognised: %v", err)
		}
		got := classifyCurReadError(err)
		if errors.Is(got, providers.ErrCostNotConfigured) {
			t.Fatalf("a role that cannot be assumed must not be filed as cost-not-configured: %v", got)
		}
		// Neither sentinel, so the spend sync marks the account not connected.
		if errors.Is(got, providers.ErrCostFetchFailedCredentialOK) {
			t.Fatalf("a role that cannot be assumed must not claim a working credential: %v", got)
		}
	})

	t.Run("a refused bucket is still cost-not-configured", func(t *testing.T) {
		err := readCostReportWithRefusedBucket(t)
		if _, refused := assumeRoleRefusal(err); refused {
			t.Fatalf("a denied bucket read was mistaken for a refused role: %v", err)
		}
		if got := classifyCurReadError(err); !errors.Is(got, providers.ErrCostNotConfigured) {
			t.Fatalf("expected ErrCostNotConfigured so the account stays connected, got %v", got)
		}
	})

	t.Run("a refused role while listing report definitions is a real failure", func(t *testing.T) {
		// The path for an account with no stored cost report config, where a
		// denied cur:DescribeReportDefinitions is otherwise the supported state.
		err := nestedAssumeRoleError("Cost and Usage Report Service", "DescribeReportDefinitions", "AccessDenied")
		if isCurAuthorizationDenied(err) {
			t.Fatal("a role that cannot be assumed must not read as a missing cost permission")
		}
	})

	t.Run("a throttled AssumeRole is not a refusal", func(t *testing.T) {
		err := nestedAssumeRoleError("S3", "GetObject", "Throttling")
		if _, refused := assumeRoleRefusal(err); refused {
			t.Fatal("a throttled sts:AssumeRole says nothing about whether the role still works")
		}
	})

	t.Run("the caller's own credentials failing is not a refusal", func(t *testing.T) {
		// AssumeRole is signed with the caller's own credentials. When those
		// cannot be fetched, the request is never sent, and the denial under it
		// belongs to the caller's identity, not to the account's role.
		ownIdentity := &smithy.OperationError{
			ServiceID:     "STS",
			OperationName: "AssumeRoleWithWebIdentity",
			Err:           &smithy.GenericAPIError{Code: "AccessDenied", Message: "Not authorized to perform sts:AssumeRoleWithWebIdentity"},
		}
		err := &smithy.OperationError{
			ServiceID:     "S3",
			OperationName: "GetObject",
			Err: fmt.Errorf("get identity: %w", fmt.Errorf("get credentials: %w", fmt.Errorf("failed to refresh cached credentials, %w", &smithy.OperationError{
				ServiceID:     "STS",
				OperationName: "AssumeRole",
				Err:           fmt.Errorf("get identity: %w", fmt.Errorf("get credentials: %w", fmt.Errorf("failed to refresh cached credentials, %w", ownIdentity))),
			}))),
		}
		if _, refused := assumeRoleRefusal(err); refused {
			t.Fatal("a failure to fetch the caller's own credentials was blamed on the account's role")
		}
	})
}

func TestExplainAssumeRoleRefusal(t *testing.T) {
	account := providers.Account{AssumeRole: aws.String(testRoleArn)}

	t.Run("a refused role is explained in plain language", func(t *testing.T) {
		cause := readCostReportWithRefusedRole(t)
		got := explainAssumeRoleRefusal(account, cause)

		for _, want := range []string{
			testRoleArn,           // which role
			"trust policy",        // what to check
			"external ID",         // what to check
			testAssumeRoleReply,   // AWS's own words, which name the caller
			"nothing can be coll", // what it means for the account
		} {
			if !strings.Contains(got.Error(), want) {
				t.Errorf("message is missing %q:\n%s", want, got.Error())
			}
		}
		// The bucket and key of whichever call happened to fetch credentials are
		// noise on the health page; they stay in the logged cause.
		if strings.Contains(got.Error(), "cur-bucket") {
			t.Errorf("message carries the wrapped call's detail:\n%s", got.Error())
		}
		if !errors.Is(got, cause) {
			t.Error("the original AWS error must stay in the unwrap chain")
		}
		if errors.Is(got, providers.ErrCostNotConfigured) || errors.Is(got, providers.ErrCostFetchFailedCredentialOK) {
			t.Error("the explained error must stay a real failure so the account is marked not connected")
		}
	})

	t.Run("other errors and nil pass through unchanged", func(t *testing.T) {
		if got := explainAssumeRoleRefusal(account, nil); got != nil {
			t.Fatalf("nil must stay nil, got %v", got)
		}
		denied := readCostReportWithRefusedBucket(t)
		if got := explainAssumeRoleRefusal(account, denied); got != denied {
			t.Fatalf("a non-credential error must be returned as is, got %v", got)
		}
		notConfigured := classifyCurReadError(denied)
		if got := explainAssumeRoleRefusal(account, notConfigured); !errors.Is(got, providers.ErrCostNotConfigured) {
			t.Fatalf("cost-not-configured must survive, got %v", got)
		}
	})
}
