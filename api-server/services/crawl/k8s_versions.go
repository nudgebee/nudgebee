package crawl

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"nudgebee/services/common"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type VersionInfo struct {
	Version     string `json:"version"`
	ReleaseDate string `json:"release_date"`
}

var (
	k8sVersionsCache struct {
		sync.RWMutex
		versions  []VersionInfo
		lastFetch time.Time
	}
	k8sVersionsCacheDuration = 24 * time.Hour
	k8sVersionTagRe          = regexp.MustCompile(`^v(\d+\.\d+)`)
)

// fetchAndProcessKubernetesVersions contains the core logic to fetch and process versions from GitHub.
func fetchAndProcessKubernetesVersions() ([]VersionInfo, error) {
	url := "https://api.github.com/repos/kubernetes/kubernetes/releases"
	var versionsMap = make(map[string]string)
	var versions []VersionInfo
	page := 1

	client := &http.Client{Transport: common.HttpClient().Transport, Timeout: 30 * time.Second}
	minSupportedMinorVersion := 25

	for {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return versions, err
		}
		query := req.URL.Query()
		query.Add("per_page", "100")
		query.Add("page", fmt.Sprintf("%d", page))
		req.URL.RawQuery = query.Encode()

		resp, err := client.Do(req)
		if err != nil {
			return versions, err
		}
		defer func() {
			err := resp.Body.Close()
			if err != nil {
				slog.Error("Error closing response body", "error", err)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			return versions, fmt.Errorf("failed to fetch data: %s", resp.Status)
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return versions, err
		}

		var releases []map[string]interface{}
		if err := common.UnmarshalJson(body, &releases); err != nil {
			return versions, err
		}

		if len(releases) == 0 {
			break
		}

		for _, release := range releases {
			if name, ok := release["tag_name"].(string); ok {
				matches := k8sVersionTagRe.FindStringSubmatch(name)
				if len(matches) > 1 {
					majorMinor := matches[1]
					minor, er := strconv.Atoi(strings.Split(majorMinor, ".")[1])
					if er != nil || minor < minSupportedMinorVersion {
						continue
					}

					publishedAt, ok := release["published_at"].(string)
					if !ok {
						continue
					}
					if existingDate, exists := versionsMap[majorMinor]; !exists || publishedAt < existingDate {
						versionsMap[majorMinor] = publishedAt
					}
				}
			}
		}
		page++
	}

	for version, releaseDate := range versionsMap {
		versions = append(versions, VersionInfo{Version: version, ReleaseDate: releaseDate})
	}
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].ReleaseDate > versions[j].ReleaseDate
	})

	return versions, nil
}

// FetchKubernetesVersionsWithDates fetches Kubernetes release versions.
// It uses an in-memory cache to avoid hitting the GitHub API on every call.
// The cache is valid for 24 hours.
func FetchKubernetesVersionsWithDates() ([]VersionInfo, error) {
	k8sVersionsCache.RLock()
	// Check if cache is valid and return it if so.
	if k8sVersionsCache.versions != nil && time.Since(k8sVersionsCache.lastFetch) < k8sVersionsCacheDuration {
		// Return a copy to prevent modification of the cached slice by the caller.
		versionsCopy := make([]VersionInfo, len(k8sVersionsCache.versions))
		copy(versionsCopy, k8sVersionsCache.versions)
		k8sVersionsCache.RUnlock()
		return versionsCopy, nil
	}
	k8sVersionsCache.RUnlock()

	// If cache is invalid or empty, acquire a write lock to repopulate it.
	k8sVersionsCache.Lock()
	defer k8sVersionsCache.Unlock()

	// Double-check if another goroutine populated the cache while we were waiting for the lock.
	if k8sVersionsCache.versions != nil && time.Since(k8sVersionsCache.lastFetch) < k8sVersionsCacheDuration {
		versionsCopy := make([]VersionInfo, len(k8sVersionsCache.versions))
		copy(versionsCopy, k8sVersionsCache.versions)
		return versionsCopy, nil
	}

	// Fetch from GitHub
	versions, err := fetchAndProcessKubernetesVersions()
	if err != nil {
		// Don't cache errors, just return them.
		return nil, err
	}

	// Update cache
	k8sVersionsCache.versions = versions
	k8sVersionsCache.lastFetch = time.Now()

	// Return a copy of the newly fetched versions.
	versionsCopy := make([]VersionInfo, len(k8sVersionsCache.versions))
	copy(versionsCopy, k8sVersionsCache.versions)
	return versionsCopy, nil
}
