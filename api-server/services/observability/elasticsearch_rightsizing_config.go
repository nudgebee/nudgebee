package observability

import (
	"fmt"
	"nudgebee/services/ml"
	"nudgebee/services/security"
)

// ElasticsearchRightsizingConfig resolves the account's Elasticsearch connection into
// the shape ml-k8s-server needs, mirroring what integrations.GetDatadogConfigs does
// for Datadog. Rightsizing lives in ml-k8s-server and queries the metrics backend
// itself, so the connection has to travel with the trigger.
//
// Cognito-signed clusters are rejected here rather than at the far end: SigV4 signing
// is Go-only today, and an unsigned request would come back 403 as "no data", which is
// the failure mode this whole change set exists to remove.
func ElasticsearchRightsizingConfig(ctx *security.RequestContext, accountId string) (*ml.ElasticsearchMetricsConfig, error) {
	cfg, err := GetElasticsearchConfig(ctx, accountId)
	if err != nil {
		return nil, err
	}
	if cfg.AuthType == "cognito" {
		return nil, fmt.Errorf("rightsizing over Elasticsearch does not support cognito authentication (account %s)", accountId)
	}
	return &ml.ElasticsearchMetricsConfig{
		Url:           cfg.Url,
		AuthType:      cfg.AuthType,
		Username:      cfg.Username,
		Password:      cfg.Password,
		ApiKey:        cfg.ApiKey,
		BearerToken:   cfg.BearerToken,
		MetricsIndex:  cfg.MetricsIndex,
		TLSSkipVerify: cfg.TLSSkipVerify,
	}, nil
}
