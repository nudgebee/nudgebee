package api

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"nudgebee/collector/otel/common"
	"nudgebee/collector/otel/config"
	"nudgebee/collector/otel/security"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/grafana/loki/v3/pkg/logql/syntax"
	"github.com/prometheus/prometheus/model/labels"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func handleLokiApis(r *gin.Engine, tracer *trace.Tracer, meter *metric.Meter, logger *slog.Logger) {

	r.GET("/loki/api/v1/query", func(c *gin.Context) {
		handleLokiRequest(c, "query", logger)
	})

	r.GET("/loki/api/v1/query_range", func(c *gin.Context) {
		handleLokiRequest(c, "query_range", logger)
	})

	r.GET("/loki/api/v1/labels", func(c *gin.Context) {
		handleLokiRequest(c, "labels", logger)
	})

	r.GET("/loki/api/v1/label/:label_name/values", func(c *gin.Context) {
		handleLokiRequest(c, "label_values", logger)
	})

	r.GET("/loki/api/v1/series", func(c *gin.Context) {
		handleLokiRequest(c, "series", logger)
	})
}

func handleLokiRequest(c *gin.Context, requestType string, logger *slog.Logger) {
	prometheusEndpoint := config.Config.OtelLogsQueryEndpoint
	if prometheusEndpoint == "" {
		logger.Error("prometheus_endpoint is not configured")
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "Internal Error"})
		return
	}

	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	authHeaderSplits := strings.Split(authHeader, " ")
	if len(authHeaderSplits) != 2 || authHeaderSplits[0] != "Basic" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	agentDetail, err := security.GetAccountFromAgentToken(authHeaderSplits[1])
	if err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	logger = logger.With("tenant_id", agentDetail.TenantId, "account_id", agentDetail.AccountId)

	status, contentType, body, err := handleLokiRequestInternal(agentDetail, requestType, c.Request.URL.Query(), c.Request.Form, c.Request.URL.Path, logger)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(status, contentType, body)
}

func handleLokiRequestInternal(agentDetail security.Account, requestType string, urlParams url.Values, formParams url.Values, path string, logger *slog.Logger) (int, string, []byte, error) {
	path = strings.Replace(path, "/loki", "", 1)
	targetURL := config.Config.OtelLogsQueryEndpoint + path
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return 500, "", nil, fmt.Errorf("logs: internal error: %w", err)
	}

	urlQuery := req.URL.Query()

	requestValues := urlParams

	for k, v := range formParams {
		for _, s := range v {
			requestValues.Add(k, s)
		}
	}

	tenantLabels := labels.FromStrings(
		OTEL_NB_TENANT_ID, agentDetail.TenantId,
		OTEL_NB_ACCOUNT_ID, agentDetail.AccountId,
	)

	switch requestType {
	case "query", "query_range":
		logql := requestValues.Get("query")
		if logql == "" {
			slog.Error("logs: query not found")
			return 500, "", nil, errors.New("logs: internal error")
		}

		// Add tenant and account id to query
		err = rewriteField("query", &urlQuery, tenantLabels, logger)
		if err != nil {
			return 400, "", nil, fmt.Errorf("logs: invalid query: %w", err)
		}
	case "series", "labels", "label_values":
		err = rewriteField("match[]", &urlQuery, tenantLabels, logger)
		if err != nil {
			return 400, "", nil, fmt.Errorf("logs: invalid query: %w", err)
		}
	}

	req.URL.RawQuery = urlQuery.Encode()
	// Forward the request to the Loki server
	client := common.HttpClient()
	resp, err := client.Do(req)
	if err != nil {
		slog.Error("logs: unable to execute query", "error", err)
		return 500, "", nil, errors.New("logs: internal error")
	}
	defer func() { _ = resp.Body.Close() }()

	// Copy the response body to the original response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.Error("logs: unable to read response", "request_type", requestType, "error", err)
		return 500, "", nil, fmt.Errorf("logs: unable to process query: %w", err)
	}

	return resp.StatusCode, resp.Header.Get("Content-Type"), respBody, nil
}

func rewriteField(field string, form *url.Values, assign labels.Labels, logger *slog.Logger) error {
	if _, ok := (*form)[field]; ok {
		for i, m := range (*form)[field] {
			q, err := logqlLabels(m, assign)
			if err == nil {
				(*form)[field][i] = q
			} else {
				return err
			}
		}
	} else {
		logger.Debug(fmt.Sprintf("%s is empty, assigning labels", field))
		x := syntax.MatchersExpr{}
		assign.Range(func(l labels.Label) {
			x.AppendMatchers([]*labels.Matcher{{
				Name:  l.Name,
				Type:  labels.MatchRegexp,
				Value: l.Value,
			}})
		})
		(*form)[field] = []string{x.String()}
	}

	return nil
}

func logqlLabels(logql string, assign labels.Labels) (string, error) {
	logql = strings.TrimSpace(logql)

	parsed, err := syntax.ParseExpr(logql)
	if err != nil {
		return "", err
	}
	parsed.Walk(func(x syntax.Expr) bool {
		switch me := x.(type) {
		case *syntax.MatchersExpr:
			assign.Range(func(l labels.Label) {
				me.AppendMatchers([]*labels.Matcher{{
					Name:  l.Name,
					Type:  labels.MatchRegexp,
					Value: l.Value,
				}})
			})
		default:
			// Do nothing
		}
		return true
	})
	return parsed.String(), nil
}
