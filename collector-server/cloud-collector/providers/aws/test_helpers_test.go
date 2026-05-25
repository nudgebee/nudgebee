package aws

import "os"

// testAWSAccountNumber is read from the TEST_AWS_ACCOUNT_NUMBER env var so test
// fixtures don't carry a hardcoded AWS account ID. Tests that build mock data
// and assert against it remain meaningful even when the variable is empty,
// because both sides of the comparison resolve to the same value.
var testAWSAccountNumber = os.Getenv("TEST_AWS_ACCOUNT_NUMBER")
