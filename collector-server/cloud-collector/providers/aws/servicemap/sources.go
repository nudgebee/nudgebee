package servicemap

import (
	"context"
	"nudgebee/collector/cloud/providers"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// RelationshipSource defines the interface for different sources of service relationship data
type RelationshipSource interface {
	// GetRelationships queries this source for service relationships
	GetRelationships(ctx context.Context, request QueryRequest) (QueryResponse, error)

	// SupportsResourceType returns true if this source can provide data for the given resource type
	SupportsResourceType(resourceType string) bool

	// Priority returns the priority of this source (lower number = higher priority)
	// Used for conflict resolution when multiple sources provide same relationship
	Priority() int

	// Name returns a human-readable name for this source
	Name() string

	// IsAvailable checks if this source is available/configured for the account
	IsAvailable(ctx context.Context, cfg aws.Config, account providers.Account) bool
}

// QueryRequest contains parameters for querying service relationships
type QueryRequest struct {
	Resources []ResourceRequest
	Region    string
	TimeRange *TimeRange // Optional: for time-based sources like Flow Logs
}

// ResourceRequest specifies a single resource to query
type ResourceRequest struct {
	ResourceID   string // ARN or resource ID
	ResourceType string // e.g., "rds", "ec2", "lambda"
	Region       string
}

// TimeRange specifies a time window for time-based data sources
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// QueryResponse contains relationships discovered by a source
type QueryResponse struct {
	Applications []providers.ServiceMapApplication
	Errors       []error
	Metadata     SourceMetadata
}

// SourceMetadata provides information about the query execution
type SourceMetadata struct {
	Source           string        // Source name
	QueriedAt        time.Time     // When the query was executed
	ExecutionTime    time.Duration // How long the query took
	ResourcesQueried int           // Number of resources successfully queried
	CacheHit         bool          // Whether results came from cache
}

// RelationshipStrength indicates confidence/importance of a relationship
type RelationshipStrength string

const (
	StrengthConfigured RelationshipStrength = "configured" // From IaC/console config
	StrengthObserved   RelationshipStrength = "observed"   // From runtime data (logs, traces)
	StrengthInferred   RelationshipStrength = "inferred"   // Derived/guessed
)

// EnrichedServiceMapApplication extends the base ServiceMapApplication with source metadata
type EnrichedServiceMapApplication struct {
	providers.ServiceMapApplication
	SourceMetadata []RelationshipMetadata
}

// RelationshipMetadata tracks which source contributed each relationship
type RelationshipMetadata struct {
	Source     string
	Strength   RelationshipStrength
	ObservedAt *time.Time
	Metrics    map[string]interface{} // e.g., bytes transferred, request count
}

// MergeStrategy defines how to combine relationships from multiple sources
type MergeStrategy interface {
	// Merge combines applications from multiple sources into a unified result
	Merge(sources []QueryResponse) ([]providers.ServiceMapApplication, error)
}

// DefaultMergeStrategy implements a simple priority-based merge
type DefaultMergeStrategy struct{}

// Merge implements the MergeStrategy interface
func (d *DefaultMergeStrategy) Merge(sources []QueryResponse) ([]providers.ServiceMapApplication, error) {
	// Deduplicate by application ID
	appMap := make(map[string]*providers.ServiceMapApplication)

	// Sort sources by metadata (lower priority number = process first)
	// Then merge relationships, with later sources enriching earlier ones

	for _, source := range sources {
		for _, app := range source.Applications {
			key := app.Id.Key()

			existing, exists := appMap[key]
			if !exists {
				// First time seeing this application
				appCopy := app
				appMap[key] = &appCopy
			} else {
				// Merge relationships
				existing.Upstreams = mergeUpstreamLinks(existing.Upstreams, app.Upstreams)
				existing.Downstreams = mergeDownstreamLinks(existing.Downstreams, app.Downstreams)

				// Update status if current is more specific
				if app.Status != "Unknown" && app.Status != "" {
					existing.Status = app.Status
				}
			}
		}
	}

	// Convert map to slice
	result := make([]providers.ServiceMapApplication, 0, len(appMap))
	for _, app := range appMap {
		result = append(result, *app)
	}

	return result, nil
}

// mergeUpstreamLinks combines two slices of UpstreamLink, merging metrics for duplicates
func mergeUpstreamLinks(a, b []providers.UpstreamLink) []providers.UpstreamLink {
	linkMap := make(map[string]*providers.UpstreamLink)

	// Add all links from a
	for _, link := range a {
		key := link.Id // Use Id directly as key since it's already a string
		linkCopy := link
		linkMap[key] = &linkCopy
	}

	// Merge links from b
	for _, link := range b {
		key := link.Id
		existing, exists := linkMap[key]
		if !exists {
			// New link, add it
			linkCopy := link
			linkMap[key] = &linkCopy
		} else {
			// Existing link, merge metrics
			existing.RequestCount += link.RequestCount
			existing.FailureCount += link.FailureCount
			existing.BytesSent += link.BytesSent
			existing.BytesReceived += link.BytesReceived

			// Keep worst status (higher value = worse)
			if link.Status > existing.Status {
				existing.Status = link.Status
			}

			// Prefer non-zero latency
			if link.Latency > 0 && existing.Latency == 0 {
				existing.Latency = link.Latency
			} else if link.Latency > 0 && existing.Latency > 0 {
				// Average latencies if both are non-zero
				existing.Latency = (existing.Latency + link.Latency) / 2
			}

			// Prefer non-empty protocol
			if existing.Protocol == "" && link.Protocol != "" {
				existing.Protocol = link.Protocol
			}
		}
	}

	// Convert map to slice
	result := make([]providers.UpstreamLink, 0, len(linkMap))
	for _, link := range linkMap {
		result = append(result, *link)
	}

	return result
}

// mergeDownstreamLinks combines two slices of DownstreamLink, merging metrics for duplicates
func mergeDownstreamLinks(a, b []providers.DownstreamLink) []providers.DownstreamLink {
	linkMap := make(map[string]*providers.DownstreamLink)

	// Add all links from a
	for _, link := range a {
		key := link.Id.Key() // Use Key() method to get string representation
		linkCopy := link
		linkMap[key] = &linkCopy
	}

	// Merge links from b
	for _, link := range b {
		key := link.Id.Key()
		existing, exists := linkMap[key]
		if !exists {
			// New link, add it
			linkCopy := link
			linkMap[key] = &linkCopy
		} else {
			// Existing link, merge metrics
			existing.RequestCount += link.RequestCount
			existing.FailureCount += link.FailureCount
			existing.BytesSent += link.BytesSent
			existing.BytesReceived += link.BytesReceived

			// Keep worst status (higher value = worse)
			if link.Status > existing.Status {
				existing.Status = link.Status
			}

			// Prefer non-zero latency
			if link.Latency > 0 && existing.Latency == 0 {
				existing.Latency = link.Latency
			} else if link.Latency > 0 && existing.Latency > 0 {
				// Average latencies if both are non-zero
				existing.Latency = (existing.Latency + link.Latency) / 2
			}

			// Prefer non-empty protocol
			if existing.Protocol == "" && link.Protocol != "" {
				existing.Protocol = link.Protocol
			}
		}
	}

	// Convert map to slice
	result := make([]providers.DownstreamLink, 0, len(linkMap))
	for _, link := range linkMap {
		result = append(result, *link)
	}

	return result
}
