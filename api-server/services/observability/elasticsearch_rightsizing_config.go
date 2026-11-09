package observability

import (
	"fmt"
	"nudgebee/services/security"
)

// CheckElasticsearchRightsizing reports whether rightsizing may run for an account
// whose metrics are in Elasticsearch. ml-k8s-server's queries go through api-server,
// which holds the connection, so this only confirms the connection can be read.
//
// Cognito-signed clusters are still refused. api-server can sign their queries, so
// this is a decision that can be lifted rather than a technical limit.
func CheckElasticsearchRightsizing(ctx *security.RequestContext, accountId string) error {
	cfg, err := GetElasticsearchConfig(ctx, accountId)
	if err != nil {
		return err
	}
	if cfg.AuthType == "cognito" {
		return fmt.Errorf("rightsizing over Elasticsearch does not support cognito authentication (account %s)", accountId)
	}
	return nil
}
