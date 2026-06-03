package gcloud

import (
	"fmt"
	"nudgebee/collector/cloud/providers"
	"strings"
	"sync"
	"time"

	serviceusage "google.golang.org/api/serviceusage/v1"
)

const enabledAPIsCacheTTL = 10 * time.Minute

// gcpServiceAPIMap maps our internal service names (lowercase) to the GCP API
// that must be enabled for the service to work. Multiple services can map to
// the same API (e.g., networking and compute engine both need compute.googleapis.com).
var gcpServiceAPIMap = map[string]string{
	"compute engine":              "compute.googleapis.com",
	"cloud storage":               "storage.googleapis.com",
	"bigquery":                    "bigquery.googleapis.com",
	"cloud sql":                   "sqladmin.googleapis.com",
	"kubernetes engine":           "container.googleapis.com",
	"cloud functions":             "cloudfunctions.googleapis.com",
	"cloud run":                   "run.googleapis.com",
	"cloud pub/sub":               "pubsub.googleapis.com",
	"cloud monitoring":            "monitoring.googleapis.com",
	"networking":                  "compute.googleapis.com",
	"vm manager":                  "osconfig.googleapis.com",
	"vertex ai":                   "aiplatform.googleapis.com",
	"gemini api":                  "generativelanguage.googleapis.com",
	"cloud load balancing":        "compute.googleapis.com",
	"recommender":                 "recommender.googleapis.com",
	"compute.googleapis.com/disk": "compute.googleapis.com",
	"compute.googleapis.com/networkinterface": "compute.googleapis.com",
	"artifact registry":                       "artifactregistry.googleapis.com",
	"iam":                                     "iam.googleapis.com",
}

type enabledAPIsCacheEntry struct {
	apis      map[string]bool
	expiresAt time.Time
}

var (
	enabledAPIsCache   = make(map[string]enabledAPIsCacheEntry)
	enabledAPIsCacheMu sync.RWMutex
)

// getEnabledAPIs returns the set of enabled GCP API names for the given project.
// Results are cached per projectId for enabledAPIsCacheTTL.
// On error, returns nil — callers should treat nil as "all services enabled".
func getEnabledAPIs(ctx providers.CloudProviderContext, session gcloudAuthSession) (map[string]bool, error) {
	if session.ProjectId == "" {
		return nil, fmt.Errorf("empty project ID")
	}

	// Check cache
	enabledAPIsCacheMu.RLock()
	entry, ok := enabledAPIsCache[session.ProjectId]
	enabledAPIsCacheMu.RUnlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.apis, nil
	}

	// Fetch from Service Usage API
	svc, err := serviceusage.NewService(ctx.GetContext(), session.Opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create service usage client: %w", err)
	}

	apis := make(map[string]bool)
	parent := fmt.Sprintf("projects/%s", session.ProjectId)
	err = svc.Services.List(parent).Filter("state:ENABLED").Fields("services/config/name", "nextPageToken").Pages(ctx.GetContext(), func(resp *serviceusage.ListServicesResponse) error {
		for _, s := range resp.Services {
			if s.Config != nil && s.Config.Name != "" {
				apis[s.Config.Name] = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list enabled services for project %s: %w", session.ProjectId, err)
	}

	// Cache result
	enabledAPIsCacheMu.Lock()
	enabledAPIsCache[session.ProjectId] = enabledAPIsCacheEntry{
		apis:      apis,
		expiresAt: time.Now().Add(enabledAPIsCacheTTL),
	}
	enabledAPIsCacheMu.Unlock()

	ctx.GetLogger().Info("fetched enabled GCP APIs", "project", session.ProjectId, "count", len(apis))
	return apis, nil
}

// isServiceEnabled checks whether the GCP API required by serviceName is enabled.
// Returns true (allow) when:
//   - enabledAPIs is nil (pre-check failed or was skipped — graceful degradation)
//   - serviceName has no mapping in gcpServiceAPIMap (unknown service — safe default)
//   - the required API is present in enabledAPIs
func isServiceEnabled(serviceName string, enabledAPIs map[string]bool) bool {
	if enabledAPIs == nil {
		return true
	}
	requiredAPI, ok := gcpServiceAPIMap[strings.ToLower(serviceName)]
	if !ok {
		// Unknown service — don't block it
		return true
	}
	return enabledAPIs[requiredAPI]
}

// clearEnabledAPIsCache removes all cached entries. Useful for testing.
func clearEnabledAPIsCache() {
	enabledAPIsCacheMu.Lock()
	enabledAPIsCache = make(map[string]enabledAPIsCacheEntry)
	enabledAPIsCacheMu.Unlock()
}
