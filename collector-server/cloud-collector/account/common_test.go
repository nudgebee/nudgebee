package account

import (
	"os"
	"testing"

	"nudgebee/collector/cloud/common"

	_ "nudgebee/collector/cloud/providers/gcloud"
	"nudgebee/collector/cloud/security"

	"github.com/stretchr/testify/assert"
)

func TestBuildArn(t *testing.T) {
	arn := buildExternalResourceId("aws", "1234", "us-east-1", "AmazonS3", "storage", "bucket", "")
	assert.Equal(t, "arn:aws:s3:us-east-1:1234:storage:bucket", arn)

	arn = buildExternalResourceId("aws", "1234", "us-east-1", "AmazonS3", "", "bucket", "")
	assert.Equal(t, "arn:aws:s3:us-east-1:1234::bucket", arn)

	arn = buildExternalResourceId("aws", "1234", "us-east-1", "AmazonS3", "storage", "bucket", "path1")
	assert.Equal(t, "arn:aws:s3:us-east-1:1234:storage:bucket/path1", arn)
}

func TestGetAgentStatus(t *testing.T) {
	ctx := security.NewRequestContextForTenantAdmin(os.Getenv("TEST_TENANT"))
	dbms, err := common.GetDatabaseManager(common.Metastore)
	assert.Nil(t, err)
	assert.NotNil(t, dbms)

	jobStatus, err := getAgentJobStatus(ctx, dbms, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotNil(t, jobStatus)
}
