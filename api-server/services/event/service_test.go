package event

import (
	"log/slog"
	"nudgebee/services/eventrule"
	"nudgebee/services/eventrule/playbooks"
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/testenv"
	"nudgebee/services/security"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInvestigateEvent(t *testing.T) {
	env := testenv.RequireEnv(t, testenv.Tenant, testenv.Account)
	tenant, account := env[testenv.Tenant], env[testenv.Account]
	start := time.UnixMilli(1755047287955)
	end := time.UnixMilli(1755050887955)
	response, err := InvestigateEvent(security.NewRequestContextForTenantAdmin(tenant, slog.Default(), nil, nil), Event{
		AccountId:      account,
		Tenant:         tenant,
		AggregationKey: "java-4xx-error-rate",
		Title:          `[Critical] Java 4xx Error Rate {environment="prod", job="eurl-service"}`,
		FindingId:      "fe46b8df-8b49-4945-ac67-45c6cb6c402c-1",
		Description:    `**Agent URL -** https://example.chronosphere.io/monitors/java-4xx-error-rate?receiver=pagerduty-orchestration-notifier&receiver-type=pagerduty&status=CRITICAL&end=1755050887955&start=1755047287955&signal=%7B%22environment%22%3A%22prod%22%2C%22job%22%3A%22eurl-service%22%7D\n **Client -** Chronosphere\n **Client URL -** https://example.chronosphere.io/monitors/java-4xx-error-rate?receiver=pagerduty-orchestration-notifier&receiver-type=pagerduty&status=CRITICAL&end=1755050887955&start=1755047287955&signal=%7B%22environment%22%3A%22prod%22%2C%22job%22%3A%22eurl-service%22%7D`,
		Fingerprint:    "Q2Q2H95LZ1JJY9",
		Cluster:        "otr-eks-staging",
		Priority:       "INFO",
		ServiceKey:     "None/None/Unresolved",
		Source:         "pagerduty_webhook",
		Status:         "CLOSED",
		SubjectName:    "Unresolved",
		Labels: map[string]string{
			"alertname":           "java-4xx-error-rate",
			"end":                 "1755050887955",
			"environment":         "prod",
			"job":                 "eurl-service",
			"nb_webhook_event_id": "Q2Q2H95LZ1JJY9",
			"nb_webhook_id":       "01FWRHI9Y2FAX9QCRPFNK2ZI7Q",
			"nb_webhook_source":   "pagerduty_webhook",
			"nb_webhook_url":      "https://api.pagerduty.com/incidents/Q2Q2H95LZ1JJY9",
			"pattern_hash":        "Q2Q2H95LZ1JJY9",
			"receiver":            "pagerduty-orchestration-notifier",
			"receiver-type":       "pagerduty",
			"rule_id":             "java-4xx-error-rate",
			"rule_type":           "static_threshold",
			"severity":            "HIGH",
			"start":               "1755047287955",
			"status":              "CRITICAL",
		},
		StartsAt: &start,
		EndsAt:   &end,
	}, "")
	assert.NotEmpty(t, response)
	assert.Nil(t, err)
	print(response)
}

func TestRefreshEvent(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant)
	tenant := os.Getenv("TEST_TENANT")
	eventId := "b33a7a82-cbcc-4cc3-a283-ff1dcd15a96c"
	err := RefreshInvestigation(security.NewRequestContextForTenantAdmin(tenant, slog.Default(), nil, nil), eventId)
	assert.Nil(t, err)
}

// TestApplyEventResolutionWithGitProvider tests the provider="git" auto-detection feature
// This test verifies that when provider="git" is passed, the system correctly detects
// GitHub or GitLab from the ci.nudgebee.com/git.repo annotation on the workload.
func TestApplyEventResolutionWithGitProvider(t *testing.T) {
	testenv.RequireEnv(t, testenv.Tenant, "APP_DATABASE_URL")
	dbms, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		t.Fatal(err)
	}

	// Test data - tenant comes from the environment (guarded by RequireEnv above).
	tenantID := os.Getenv("TEST_TENANT")

	ctx := security.NewRequestContextForTenantAdmin(tenantID, slog.Default(), nil, nil)

	t.Run("git provider auto-detects from annotation", func(t *testing.T) {
		// Find an event with a resource that has git.repo annotation
		var eventID, resourceID, cloudAccountID, accountID string
		err := dbms.Db.QueryRow(`
			SELECT e.id, e.cloud_resource_id, e.cloud_account_id, a.id
			FROM events e
			JOIN cloud_accounts a ON e.cloud_account_id = a.id
			JOIN cloud_resourses cr ON e.cloud_resource_id = cr.id
			WHERE e.tenant = $1
			  AND e.cloud_resource_id IS NOT NULL
			  AND e.cloud_account_id IS NOT NULL
			  AND a.account_type = 'kubernetes'
			LIMIT 1
		`, tenantID).Scan(&eventID, &resourceID, &cloudAccountID, &accountID)

		if err != nil {
			t.Skipf("No suitable event found for git provider test: %v", err)
		}

		// Check if there's a workload with git.repo annotation for this resource
		var gitRepoURL string
		err = dbms.Db.QueryRow(`
			SELECT w.meta::json->'config'->'annotations'->>'ci.nudgebee.com/git.repo'
			FROM k8s_workloads w
			JOIN cloud_resourses cr ON cr.account = w.cloud_account_id
			  AND cr.meta::json->>'namespace' = w.namespace
			  AND cr.meta::json->>'controller' = w.name
			WHERE cr.id = $1
			  AND w.is_active = true
			  AND w.meta::json->'config'->'annotations'->>'ci.nudgebee.com/git.repo' IS NOT NULL
			LIMIT 1
		`, resourceID).Scan(&gitRepoURL)

		if err != nil || gitRepoURL == "" {
			t.Skipf("No workload with git.repo annotation found for resource %s", resourceID)
		}

		t.Logf("Found event %s with git.repo annotation: %s", eventID, gitRepoURL)

		// Determine expected provider from URL
		expectedProvider := ""
		if strings.Contains(gitRepoURL, "github.com") || strings.HasPrefix(gitRepoURL, "github.") {
			expectedProvider = "github"
		} else if strings.Contains(gitRepoURL, "gitlab.com") || strings.Contains(gitRepoURL, "gitlab.") {
			expectedProvider = "gitlab"
		}

		if expectedProvider == "" {
			t.Skipf("Git repo URL %s is not GitHub or GitLab, skipping test", gitRepoURL)
		}

		t.Logf("Expected provider detection: %s", expectedProvider)

		// Create request with provider="git" to trigger auto-detection
		request := EventRecommendationApplyRequest{
			AccountId: accountID,
			EventId:   eventID,
			Provider:  "git", // This should auto-detect to github or gitlab
			ProviderConfig: map[string]any{
				"recommendation_source": "event",
				"name":                  "nudgebee",
			},
			Data: map[string]any{
				"reason": "Testing git provider auto-detection for event",
			},
		}

		// Apply the event resolution
		_, err = ApplyEventResolution(ctx, request)

		// The request may fail due to missing credentials, but provider detection should work
		if err != nil {
			errMsg := err.Error()
			if strings.Contains(errMsg, "unable to detect git provider") {
				t.Errorf("Git provider detection failed: %v", err)
			} else if strings.Contains(errMsg, "annotation not found") {
				t.Errorf("Git repo annotation not found: %v", err)
			} else {
				t.Logf("Request failed as expected (likely missing credentials): %v", err)
				t.Logf("✓ Provider detection succeeded - %s was detected from annotation", expectedProvider)
			}
		} else {
			t.Logf("✓ Request succeeded with auto-detected provider: %s", expectedProvider)
		}
	})

	t.Run("git provider fails without resource", func(t *testing.T) {
		// Find an event without cloud_resource_id
		var eventID, accountID string
		err := dbms.Db.QueryRow(`
			SELECT e.id, a.id
			FROM events e
			JOIN cloud_accounts a ON e.cloud_account_id = a.id
			WHERE e.tenant = $1
			  AND e.cloud_resource_id IS NULL
			  AND a.account_type = 'kubernetes'
			LIMIT 1
		`, tenantID).Scan(&eventID, &accountID)

		if err != nil {
			t.Skipf("No event without resource found: %v", err)
		}

		t.Logf("Testing with event %s (no cloud_resource_id)", eventID)

		request := EventRecommendationApplyRequest{
			AccountId: accountID,
			EventId:   eventID,
			Provider:  "git",
			ProviderConfig: map[string]any{
				"recommendation_source": "event",
			},
			Data: map[string]any{
				"reason": "Testing git provider without resource",
			},
		}

		_, err = ApplyEventResolution(ctx, request)

		require.Error(t, err, "Should fail when resource is not found")
		assert.True(t, strings.Contains(err.Error(), "resource not found"),
			"Error should indicate resource not found: %v", err)
		t.Logf("✓ Correctly failed with expected error: %v", err)
	})

	t.Run("git provider fails without annotation", func(t *testing.T) {
		// Find an event with resource but without git.repo annotation
		var eventID, accountID string
		err := dbms.Db.QueryRow(`
			SELECT e.id, a.id
			FROM events e
			JOIN cloud_accounts a ON e.cloud_account_id = a.id
			JOIN cloud_resourses cr ON e.cloud_resource_id = cr.id
			LEFT JOIN k8s_workloads w ON cr.account = w.cloud_account_id
			  AND cr.meta::json->>'namespace' = w.namespace
			  AND cr.meta::json->>'controller' = w.name
			  AND w.is_active = true
			WHERE e.tenant = $1
			  AND e.cloud_resource_id IS NOT NULL
			  AND a.account_type = 'kubernetes'
			  AND (w.id IS NULL OR w.meta::json->'config'->'annotations'->>'ci.nudgebee.com/git.repo' IS NULL)
			LIMIT 1
		`, tenantID).Scan(&eventID, &accountID)

		if err != nil {
			t.Skipf("No event without git.repo annotation found: %v", err)
		}

		t.Logf("Testing with event %s (no git.repo annotation)", eventID)

		request := EventRecommendationApplyRequest{
			AccountId: accountID,
			EventId:   eventID,
			Provider:  "git",
			ProviderConfig: map[string]any{
				"recommendation_source": "event",
			},
			Data: map[string]any{
				"reason": "Testing git provider without annotation",
			},
		}

		_, err = ApplyEventResolution(ctx, request)

		require.Error(t, err, "Should fail when git.repo annotation is missing")
		assert.True(t,
			strings.Contains(err.Error(), "annotation not found") ||
				strings.Contains(err.Error(), "controller kind not found") ||
				strings.Contains(err.Error(), "workload not found"),
			"Error should indicate missing annotation or workload: %v", err)
		t.Logf("✓ Correctly failed with expected error: %v", err)
	})

	t.Run("empty provider defaults to kubernetes", func(t *testing.T) {
		// Find a kubernetes event
		var eventID, accountID string
		err := dbms.Db.QueryRow(`
			SELECT e.id, a.id
			FROM events e
			JOIN cloud_accounts a ON e.cloud_account_id = a.id
			WHERE e.tenant = $1
			  AND a.account_type = 'kubernetes'
			LIMIT 1
		`, tenantID).Scan(&eventID, &accountID)

		if err != nil {
			t.Skipf("No kubernetes event found: %v", err)
		}

		request := EventRecommendationApplyRequest{
			AccountId: accountID,
			EventId:   eventID,
			Provider:  "", // Empty provider - should default to kubernetes
			ProviderConfig: map[string]any{
				"recommendation_source": "event",
			},
			Data: map[string]any{
				"reason": "Testing empty provider defaults to kubernetes",
			},
		}

		_, err = ApplyEventResolution(ctx, request)

		// Should attempt kubernetes adapter (may fail for other reasons)
		if err != nil {
			// Should NOT be annotation-related error
			assert.False(t, strings.Contains(err.Error(), "annotation not found"),
				"Empty provider should use kubernetes adapter, not require annotation")
			t.Logf("✓ Empty provider correctly defaulted to kubernetes (error: %v)", err)
		} else {
			t.Logf("✓ Empty provider correctly used kubernetes adapter")
		}
	})
}

func TestTruncateStringToMaxBytes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		max      int
		expected string
	}{
		{
			name:     "short ASCII string unchanged",
			input:    "hello",
			max:      10,
			expected: "hello",
		},
		{
			name:     "exact length unchanged",
			input:    "hello",
			max:      5,
			expected: "hello",
		},
		{
			name:     "truncates long ASCII string",
			input:    "hello world",
			max:      5,
			expected: "hello",
		},
		{
			name:     "empty string unchanged",
			input:    "",
			max:      10,
			expected: "",
		},
		{
			name:     "preserves 2-byte UTF-8 boundary",
			input:    "abc\u00e9def", // é is 2 bytes (0xC3 0xA9)
			max:      4,              // "abc" = 3 bytes, é starts at byte 3, needs 2 bytes
			expected: "abc",          // can't fit é, so just "abc"
		},
		{
			name:     "preserves 3-byte UTF-8 boundary",
			input:    "ab\u4e16\u754c", // 世界 - each 3 bytes
			max:      5,                // "ab" = 2 bytes, 世 = 3 bytes = 5 total
			expected: "ab\u4e16",       // fits exactly
		},
		{
			name:     "cuts mid 3-byte char",
			input:    "ab\u4e16\u754c", // 世界 - each 3 bytes
			max:      4,                // "ab" = 2 bytes, only 2 bytes left for 世 (needs 3)
			expected: "ab",
		},
		{
			name:     "preserves 4-byte UTF-8 boundary",
			input:    "a\U0001F600b", // 😀 is 4 bytes
			max:      5,              // "a" = 1 byte, 😀 = 4 bytes = 5 total
			expected: "a\U0001F600",
		},
		{
			name:     "cuts mid 4-byte char",
			input:    "a\U0001F600b", // 😀 is 4 bytes
			max:      3,              // "a" = 1 byte, only 2 bytes left for 😀 (needs 4)
			expected: "a",
		},
		{
			name:     "max 0 returns empty",
			input:    "hello",
			max:      0,
			expected: "",
		},
		{
			name:     "handles long repeated string",
			input:    strings.Repeat("a", 3000),
			max:      2048,
			expected: strings.Repeat("a", 2048),
		},
		{
			name:     "handles long repeated multibyte string",
			input:    strings.Repeat("\u4e16", 1000), // 3000 bytes
			max:      2048,
			expected: strings.Repeat("\u4e16", 682), // 682 * 3 = 2046, next would be 2049
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateStringToMaxBytes(tt.input, tt.max)
			assert.Equal(t, tt.expected, result)
			assert.LessOrEqual(t, len(result), tt.max, "result byte length must not exceed max")
			// Verify result is valid UTF-8
			assert.True(t, isValidUTF8(result), "result must be valid UTF-8")
		})
	}
}

func TestMaxSubjectNameBytesConstant(t *testing.T) {
	assert.Equal(t, 2048, maxSubjectNameBytes)
}

func TestPickTopMatchingSeries_SeriesListResult(t *testing.T) {
	data := map[string]any{
		"series_list_result": []any{
			map[string]any{
				"metric": map[string]any{
					"app_id":       "/k8s/newrelic/newrelic-bundle-nri-kube-events",
					"container_id": "/k8s/newrelic/newrelic-bundle-nri-kube-events-75764df785-px254/forwarder",
					"sample":       "level=error msg=\"could not queue event\"",
				},
				"values": []any{"543", "557", "1024"},
			},
			map[string]any{
				"metric": map[string]any{
					"app_id":       "/k8s/app-162/payments-service",
					"container_id": "/k8s/app-162/payments-service-7bc6fbbc9b-m5j56/payments-service",
					"sample":       "ERROR - Health check failed",
				},
				"values": []any{"8", "15"},
			},
		},
	}

	result := pickTopMatchingSeries(data, "app_id", "/k8s/newrelic/newrelic-bundle-nri-kube-events")
	assert.NotNil(t, result)
	assert.Equal(t, "/k8s/newrelic/newrelic-bundle-nri-kube-events-75764df785-px254/forwarder", result["container_id"])
	assert.Equal(t, "level=error msg=\"could not queue event\"", result["sample"])
}

func TestPickTopMatchingSeries_VectorResult(t *testing.T) {
	data := map[string]any{
		"vector_result": []any{
			map[string]any{
				"metric": map[string]any{
					"destination_workload_name": "ingress-nginx-controller",
					"container_id":              "/k8s/ingress-nginx/ingress-nginx-controller-abc/controller",
					"path":                      "/api/v1/health",
					"method":                    "GET",
					"status":                    "503",
				},
				"value": []any{1709827200.0, "12"},
			},
			map[string]any{
				"metric": map[string]any{
					"destination_workload_name": "other-service",
					"container_id":              "/k8s/default/other-service-xyz/service",
					"path":                      "/api/data",
					"method":                    "POST",
					"status":                    "500",
				},
				"value": []any{1709827200.0, "5"},
			},
		},
	}

	result := pickTopMatchingSeries(data, "destination_workload_name", "ingress-nginx-controller")
	assert.NotNil(t, result)
	assert.Equal(t, "/k8s/ingress-nginx/ingress-nginx-controller-abc/controller", result["container_id"])
	assert.Equal(t, "503", result["status"])
}

func TestPickTopMatchingSeries_WrappedUnderKeyA(t *testing.T) {
	data := map[string]any{
		"A": map[string]any{
			"series_list_result": []any{
				map[string]any{
					"metric": map[string]any{
						"app_id":       "/k8s/test/my-app",
						"container_id": "/k8s/test/my-app-pod/container",
						"sample":       "ERROR - Connection timeout",
					},
					"values": []any{"3", "7"},
				},
			},
		},
	}

	result := pickTopMatchingSeries(data, "app_id", "/k8s/test/my-app")
	assert.NotNil(t, result)
	assert.Equal(t, "/k8s/test/my-app-pod/container", result["container_id"])
}

func TestPickTopMatchingSeries_NoMatch(t *testing.T) {
	data := map[string]any{
		"series_list_result": []any{
			map[string]any{
				"metric": map[string]any{"app_id": "/k8s/other/app", "container_id": "x"},
				"values": []any{"10"},
			},
		},
	}

	result := pickTopMatchingSeries(data, "app_id", "/k8s/nonexistent/app")
	assert.Nil(t, result)
}

func TestPickTopMatchingSeries_PicksHighestValue(t *testing.T) {
	data := map[string]any{
		"vector_result": []any{
			map[string]any{
				"metric": map[string]any{"app_id": "/k8s/test/app", "container_id": "low", "sample": "low-sample"},
				"value":  []any{1.0, "2"},
			},
			map[string]any{
				"metric": map[string]any{"app_id": "/k8s/test/app", "container_id": "high", "sample": "high-sample"},
				"value":  []any{1.0, "20"},
			},
			map[string]any{
				"metric": map[string]any{"app_id": "/k8s/test/app", "container_id": "mid", "sample": "mid-sample"},
				"value":  []any{1.0, "8"},
			},
		},
	}

	result := pickTopMatchingSeries(data, "app_id", "/k8s/test/app")
	assert.NotNil(t, result)
	assert.Equal(t, "high", result["container_id"])
	assert.Equal(t, "high-sample", result["sample"])
}

func TestPickTopMatchingSeries_EmptyData(t *testing.T) {
	assert.Nil(t, pickTopMatchingSeries(map[string]any{}, "app_id", "x"))
	assert.Nil(t, pickTopMatchingSeries(map[string]any{"series_list_result": []any{}}, "app_id", "x"))
}

func TestMergeAggregatedAlertLabels_FallbackToPrometheusEnricher(t *testing.T) {
	labels := map[string]string{
		"app_id": "/k8s/newrelic/newrelic-bundle-nri-kube-events",
	}

	evidenceResponse := []eventrule.PlaybookActionExecutionResponse{
		{
			ActionName: "prometheus_enricher",
			Response: playbooks.PrometheusActionResponse{
				Data: map[string]any{
					"series_list_result": []any{
						map[string]any{
							"metric": map[string]any{
								"app_id":       "/k8s/newrelic/newrelic-bundle-nri-kube-events",
								"container_id": "/k8s/newrelic/newrelic-bundle-nri-kube-events-75764df785-px254/forwarder",
								"sample":       "level=error msg=\"could not queue event\"",
							},
							"values": []any{"543", "1024"},
						},
						map[string]any{
							"metric": map[string]any{
								"app_id":       "/k8s/app-162/payments-service",
								"container_id": "/k8s/app-162/payments-service-7bc6fbbc9b-m5j56/payments-service",
								"sample":       "ERROR - Health check failed",
							},
							"values": []any{"8", "15"},
						},
					},
				},
			},
		},
	}

	updated := mergeAggregatedAlertLabels(labels, "HighErrorCriticalLogs", evidenceResponse)
	assert.True(t, updated)
	assert.Equal(t, "/k8s/newrelic/newrelic-bundle-nri-kube-events-75764df785-px254/forwarder", labels["container_id"])
	assert.Equal(t, "level=error msg=\"could not queue event\"", labels["sample"])
}

func TestMergeAggregatedAlertLabels_NonAggregatedKeySkipped(t *testing.T) {
	labels := map[string]string{"app_id": "test"}
	evidenceResponse := []eventrule.PlaybookActionExecutionResponse{
		{ActionName: "prometheus_enricher", Response: playbooks.PrometheusActionResponse{Data: map[string]any{}}},
	}

	updated := mergeAggregatedAlertLabels(labels, "SomeOtherAlert", evidenceResponse)
	assert.False(t, updated)
}

// isValidUTF8 checks if the string is valid UTF-8 without importing unicode/utf8
// (strings package is already imported).
func isValidUTF8(s string) bool {
	for i := 0; i < len(s); {
		r := s[i]
		if r < 0x80 {
			i++
			continue
		}
		var size int
		switch {
		case r&0xE0 == 0xC0:
			size = 2
		case r&0xF0 == 0xE0:
			size = 3
		case r&0xF8 == 0xF0:
			size = 4
		default:
			return false
		}
		if i+size > len(s) {
			return false
		}
		for j := 1; j < size; j++ {
			if s[i+j]&0xC0 != 0x80 {
				return false
			}
		}
		i += size
	}
	return true
}
