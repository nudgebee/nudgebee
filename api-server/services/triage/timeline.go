package triage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"nudgebee/services/internal/database/models"

	"github.com/jmoiron/sqlx"
	"golang.org/x/sync/errgroup"
)

// TimelineEntry represents a single entry in the event timeline
type TimelineEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	RefType   string                 `json:"ref_type"` // "event", "workload", "git_commit", "config_change"
	RefID     string                 `json:"ref_id"`
	Action    string                 `json:"action"`
	Summary   string                 `json:"summary"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"` // Additional data for navigation (e.g., cloud_account_id, namespace)
}

// EventTimeline represents the complete timeline for an event
type EventTimeline struct {
	EventID  string          `json:"event_id"`
	Timeline []TimelineEntry `json:"timeline"`
}

// BuildEventTimeline constructs a chronological timeline of all actions related to an event
func BuildEventTimeline(ctx context.Context, db *sqlx.DB, eventID string, tenantID string) (*EventTimeline, error) {
	// Get main event first (required for all other queries)
	event, err := getEventByID(ctx, db, eventID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to get event: %w", err)
	}

	timeline := &EventTimeline{
		EventID:  eventID,
		Timeline: []TimelineEntry{},
	}

	// Use mutex to safely append to timeline from multiple goroutines
	var mu sync.Mutex
	appendEntries := func(entries []TimelineEntry) {
		mu.Lock()
		timeline.Timeline = append(timeline.Timeline, entries...)
		mu.Unlock()
	}

	// Run all timeline queries in parallel
	g, gCtx := errgroup.WithContext(ctx)

	// Add main event entries (fired/resolved, duplicates, history)
	g.Go(func() error {
		entries := getMainEventEntries(gCtx, db, event, tenantID)
		appendEntries(entries)
		return nil
	})

	// Add correlated events
	g.Go(func() error {
		entries := getCorrelatedEventEntries(gCtx, db, event, tenantID)
		appendEntries(entries)
		return nil
	})

	// Add configuration changes
	g.Go(func() error {
		entries := getConfigChangeEntries(gCtx, db, event)
		appendEntries(entries)
		return nil
	})

	// Add workload info
	g.Go(func() error {
		entries := getWorkloadEntries(gCtx, db, event)
		appendEntries(entries)
		return nil
	})

	// Add recent events on the same resource (e.g., DB backups, maintenance, state changes)
	g.Go(func() error {
		entries := getSameResourceEventEntries(gCtx, db, event, tenantID)
		appendEntries(entries)
		return nil
	})

	// Wait for all goroutines to complete
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("failed to build timeline: %w", err)
	}

	// Dedupe entries that refer to the same underlying ref (typically event ID).
	// The five bucket queries can each pick up the same event through different
	// relationships — a configuration_change event that is also correlated to
	// the main event and also lives on the same resource ends up as three rows
	// (Config Change + Correlated Alert + generic Event) even though it is one
	// underlying record. Keep only the highest-priority entry per RefID so the
	// UI renders one row per event with the most informative Action.
	timeline.Timeline = dedupeTimelineEntries(timeline.Timeline)

	// Sort by timestamp
	sort.Slice(timeline.Timeline, func(i, j int) bool {
		return timeline.Timeline[i].Timestamp.Before(timeline.Timeline[j].Timestamp)
	})

	return timeline, nil
}

// timelineActionPriority ranks Action values so the most informative entry
// wins when multiple buckets emit rows for the same RefID. Lower value = higher
// priority. Actions absent from this map fall through to timelineActionDefault
// below all explicit entries (preserves them but loses to any explicit one).
//
//	configuration_changed  — tells the user what actually changed
//	first_occurrence       — duplicate chain origin (long-lived context)
//	fired                  — the main event row
//	alert_fired            — candidate came from the correlation edge
//	resolved               — main event resolution
//	created                — workload / deploy creation
//	related_resource_event — weakest: generic same-resource match
var timelineActionPriority = map[string]int{
	"configuration_changed":  0,
	"first_occurrence":       1,
	"fired":                  2,
	"alert_fired":            3,
	"resolved":               4,
	"created":                5,
	"related_resource_event": 6,
}

const timelineActionDefault = 100

// dedupeTimelineEntries keeps one entry per RefID, preferring the entry whose
// Action ranks highest in timelineActionPriority. Different buckets tag the
// same underlying event with different (RefType, Action) pairs — e.g. a
// configuration_change event that is also correlated and also surfaces via the
// same-resource query produces three rows. The Action priority picks the most
// informative angle (usually `configuration_changed` over `alert_fired` over
// `related_resource_event`) and its RefType comes along.
//
// Two classes of entries bypass dedup:
//   - empty RefID: annotation-style rows with no first-class record to collapse
//     against
//   - RefType=="event_history": lifecycle entries (status_changed, commented,
//     priority_changed, assigned, resolved, …) all share the main event's ID as
//     RefID but represent distinct sequential occurrences. Collapsing them would
//     erase the history of how the event was handled.
func dedupeTimelineEntries(entries []TimelineEntry) []TimelineEntry {
	if len(entries) <= 1 {
		return entries
	}
	priority := func(action string) int {
		if p, ok := timelineActionPriority[action]; ok {
			return p
		}
		return timelineActionDefault
	}
	best := make(map[string]int, len(entries)) // RefID → index into out
	out := make([]TimelineEntry, 0, len(entries))
	for _, e := range entries {
		// Pass through entries that shouldn't be collapsed against a RefID bucket.
		if e.RefID == "" || e.RefType == "event_history" {
			out = append(out, e)
			continue
		}
		if idx, seen := best[e.RefID]; seen {
			if priority(e.Action) < priority(out[idx].Action) {
				out[idx] = e
			}
			continue
		}
		best[e.RefID] = len(out)
		out = append(out, e)
	}
	return out
}

// getEventByID retrieves an event by its ID with tenant isolation
func getEventByID(ctx context.Context, db *sqlx.DB, eventID string, tenantID string) (*models.Event, error) {
	query := `
		SELECT
			id, title, finding_type, category,
			subject_type, subject_name, subject_namespace,
			subject_owner, subject_owner_kind,
			fingerprint, starts_at, ends_at, status,
			cloud_account_id, cloud_resource_id, source,
			tenant, created_at
		FROM events
		WHERE id = $1 AND tenant = $2
	`

	var event models.Event
	err := db.GetContext(ctx, &event, query, eventID, tenantID)
	if err != nil {
		return nil, err
	}

	return &event, nil
}

// getMainEventEntries returns entries for the main event (fired and resolved)
// Also shows first occurrence if this event is part of a duplicate chain
func getMainEventEntries(ctx context.Context, db *sqlx.DB, event *models.Event, tenantID string) []TimelineEntry {
	var entries []TimelineEntry

	// Check if this event has a first occurrence (is part of duplicate chain)
	var firstOccurrence struct {
		FirstEventID  string    `db:"first_event_id"`
		FirstStartsAt time.Time `db:"first_starts_at"`
		OccurrenceNum int       `db:"occurrence_number"`
	}

	err := db.GetContext(ctx, &firstOccurrence, `
		SELECT
			ed.first_event_id,
			e.starts_at as first_starts_at,
			ed.occurrence_number
		FROM event_duplicates ed
		JOIN events e ON e.id = ed.first_event_id
		WHERE ed.event_id = $1 AND ed.tenant_id = $2
	`, event.Id, tenantID)

	// If this event is part of a duplicate chain and not the first occurrence, show first occurrence
	if err == nil && firstOccurrence.FirstEventID != event.Id && firstOccurrence.OccurrenceNum > 1 {
		entries = append(entries, TimelineEntry{
			Timestamp: firstOccurrence.FirstStartsAt,
			RefType:   "event",
			RefID:     firstOccurrence.FirstEventID,
			Action:    "first_occurrence",
			Summary:   event.Title,
		})
	}

	// Fallback: Check for older events with same fingerprint (handles events created before duplicate tracking)
	// This runs when: (1) not in event_duplicates, OR (2) marked as occurrence #1 but might have older untracked events
	if event.Fingerprint != nil && event.CloudAccountId != nil && event.StartsAt != nil {
		var olderEvent struct {
			ID       string    `db:"id"`
			StartsAt time.Time `db:"starts_at"`
			Title    string    `db:"title"`
		}

		fallbackErr := db.GetContext(ctx, &olderEvent, `
			SELECT id, starts_at, title
			FROM events
			WHERE fingerprint = $1
			  AND cloud_account_id = $2
			  AND starts_at < $3
			  AND id != $4
			ORDER BY starts_at ASC
			LIMIT 1
		`, *event.Fingerprint, *event.CloudAccountId, *event.StartsAt, event.Id)

		// If found an older event with same fingerprint, show it as first occurrence
		// Only add if we didn't already add a first occurrence from event_duplicates
		alreadyAddedFromDuplicates := err == nil && firstOccurrence.FirstEventID != event.Id
		if fallbackErr == nil && !alreadyAddedFromDuplicates {
			entries = append(entries, TimelineEntry{
				Timestamp: olderEvent.StartsAt,
				RefType:   "event",
				RefID:     olderEvent.ID,
				Action:    "first_occurrence",
				Summary:   olderEvent.Title,
			})
		}
	}

	// Always show the current event's fired entry
	if event.StartsAt != nil {
		entries = append(entries, TimelineEntry{
			Timestamp: *event.StartsAt,
			RefType:   "event",
			RefID:     event.Id,
			Action:    "fired",
			Summary:   event.Title,
		})
	}

	// Add event history entries (status changes, urgency, priority, assignments, etc.)
	historyEntries := getEventHistoryEntries(ctx, db, event, tenantID)
	entries = append(entries, historyEntries...)

	return entries
}

// getEventHistoryEntries returns entries from event_history table with tenant isolation
func getEventHistoryEntries(ctx context.Context, db *sqlx.DB, event *models.Event, tenantID string) []TimelineEntry {
	query := `
		SELECT
			changed_at,
			change_type,
			change_reason,
			old_value,
			new_value,
			metadata
		FROM event_history
		WHERE event_id = $1 AND tenant_id = $2
		ORDER BY changed_at ASC
	`

	var historyEntries []struct {
		ChangedAt    time.Time       `db:"changed_at"`
		ChangeType   string          `db:"change_type"`
		ChangeReason *string         `db:"change_reason"`
		OldValue     json.RawMessage `db:"old_value"`
		NewValue     json.RawMessage `db:"new_value"`
		Metadata     json.RawMessage `db:"metadata"`
	}

	err := db.SelectContext(ctx, &historyEntries, query, event.Id, tenantID)
	if err != nil {
		slog.WarnContext(ctx, "Failed to get event history", "error", err, "event_id", event.Id)
		return nil
	}

	var entries []TimelineEntry
	for _, entry := range historyEntries {
		summary := formatHistoryEntrySummary(entry.ChangeType, entry.ChangeReason, entry.OldValue, entry.NewValue, entry.Metadata)
		action := formatHistoryAction(entry.ChangeType)

		entries = append(entries, TimelineEntry{
			Timestamp: entry.ChangedAt,
			RefType:   "event_history",
			RefID:     event.Id,
			Action:    action,
			Summary:   summary,
		})
	}
	return entries
}

// formatHistoryAction converts change_type to timeline action
func formatHistoryAction(changeType string) string {
	switch changeType {
	case "status":
		return "status_changed"
	case "urgency":
		return "urgency_changed"
	case "priority":
		return "priority_changed"
	case "assignment":
		return "assigned"
	case "comment":
		return "commented"
	default:
		return changeType + "_changed"
	}
}

// formatHistoryEntrySummary creates a human-readable summary from event history entry
func formatHistoryEntrySummary(changeType string, changeReason *string, oldValue, newValue, metadata json.RawMessage) string {
	var oldStr, newStr string
	_ = json.Unmarshal(oldValue, &oldStr)
	_ = json.Unmarshal(newValue, &newStr)

	switch changeType {
	case "status":
		if changeReason != nil && *changeReason == "alert_resolved" {
			// Check metadata for resolution method
			var meta map[string]interface{}
			if err := json.Unmarshal(metadata, &meta); err == nil {
				if method, ok := meta["resolution_method"].(string); ok {
					return fmt.Sprintf("Status: %s → %s (%s)", oldStr, newStr, method)
				}
			}
		}
		return fmt.Sprintf("Status: %s → %s", oldStr, newStr)
	case "urgency":
		return fmt.Sprintf("Urgency: %s → %s", oldStr, newStr)
	case "priority":
		return fmt.Sprintf("Priority: %s → %s", oldStr, newStr)
	case "assignment":
		if oldStr == "" {
			return fmt.Sprintf("Assigned to %s", newStr)
		}
		return fmt.Sprintf("Reassigned: %s → %s", oldStr, newStr)
	case "comment":
		return fmt.Sprintf("Comment added: %s", newStr)
	default:
		return fmt.Sprintf("%s: %s → %s", changeType, oldStr, newStr)
	}
}

// getCorrelatedEventEntries returns entries for correlated events
// Excludes Anomaly and SLO events from the timeline
func getCorrelatedEventEntries(ctx context.Context, db *sqlx.DB, event *models.Event, tenantID string) []TimelineEntry {
	correlations, err := GetCorrelatedEvents(ctx, db, event.Id, tenantID)
	if err != nil || len(correlations) == 0 {
		return nil
	}

	var entries []TimelineEntry
	for _, corr := range correlations {
		// Skip Anomaly and SLO events
		if corr.CorrelatedFindingType != nil {
			findingType := strings.ToUpper(*corr.CorrelatedFindingType)
			if findingType == "ANOMALY" || findingType == "SLO" {
				continue
			}
		}

		entries = append(entries, TimelineEntry{
			Timestamp: corr.CorrelatedStartsAt,
			RefType:   "event",
			RefID:     corr.CorrelatedEventID,
			Action:    "alert_fired",
			Summary:   corr.CorrelatedTitle,
		})
	}
	return entries
}

// getConfigChangeEntries returns entries for configuration changes
func getConfigChangeEntries(ctx context.Context, db *sqlx.DB, event *models.Event) []TimelineEntry {
	if event.SubjectOwner == nil || event.SubjectNamespace == nil || event.StartsAt == nil || event.CloudAccountId == nil {
		return nil
	}

	// Look back 7 days before alert, and 30 minutes after (to catch changes around alert time)
	startWindow := event.StartsAt.Add(-7 * 24 * time.Hour)
	endWindow := event.StartsAt.Add(30 * time.Minute)

	// Find last deployment change before the alert
	query := `
		SELECT id, title, description, subject_name, starts_at, evidences
		FROM events
		WHERE finding_type = 'configuration_change'
		  AND cloud_account_id = $1
		  AND subject_namespace = $2
		  AND (subject_name = $3 OR subject_owner = $3)
		  AND starts_at >= $4
		  AND starts_at <= $5
		ORDER BY starts_at DESC
		LIMIT 1
	`

	var changes []struct {
		ID          string          `db:"id"`
		Title       string          `db:"title"`
		Description string          `db:"description"`
		SubjectName string          `db:"subject_name"`
		StartsAt    time.Time       `db:"starts_at"`
		Evidences   json.RawMessage `db:"evidences"`
	}

	err := db.SelectContext(ctx, &changes, query,
		*event.CloudAccountId,
		*event.SubjectNamespace,
		*event.SubjectOwner,
		startWindow,
		endWindow,
	)
	if err != nil {
		slog.WarnContext(ctx, "Failed to get config changes", "error", err)
		return nil
	}

	var entries []TimelineEntry
	for _, change := range changes {
		summary := extractConfigChangeSummary(change.Evidences, change.Title)
		entries = append(entries, TimelineEntry{
			Timestamp: change.StartsAt,
			RefType:   "config_change",
			RefID:     change.ID,
			Action:    "configuration_changed",
			Summary:   summary,
		})
	}
	return entries
}

// getWorkloadEntries returns entries for workload creation
func getWorkloadEntries(ctx context.Context, db *sqlx.DB, event *models.Event) []TimelineEntry {
	if event.SubjectOwner == nil || event.SubjectNamespace == nil || event.CloudAccountId == nil {
		return nil
	}

	query := `
		SELECT
			cloud_resource_id, name, namespace, kind, creation_time
		FROM k8s_workloads
		WHERE cloud_account_id = $1
		  AND name = $2
		  AND namespace = $3
		LIMIT 1
	`

	var workload struct {
		CloudResourceID string    `db:"cloud_resource_id"`
		Name            string    `db:"name"`
		Namespace       string    `db:"namespace"`
		Kind            string    `db:"kind"`
		CreationTime    time.Time `db:"creation_time"`
	}

	err := db.GetContext(ctx, &workload, query,
		*event.CloudAccountId,
		*event.SubjectOwner,
		*event.SubjectNamespace,
	)
	if err != nil {
		slog.WarnContext(ctx, "Failed to get workload", "error", err)
		return nil
	}

	// Return workload creation entry with metadata for navigation
	return []TimelineEntry{{
		Timestamp: workload.CreationTime,
		RefType:   "workload",
		RefID:     workload.CloudResourceID,
		Action:    "created",
		Summary:   fmt.Sprintf("%s %s created", workload.Kind, workload.Name),
		Metadata: map[string]interface{}{
			"cloud_account_id": *event.CloudAccountId,
			"namespace":        workload.Namespace,
			"workload_name":    workload.Name,
		},
	}}
}

// getSameResourceEventEntries returns recent events on the same cloud resource.
// This surfaces related activity like DB backups, maintenance windows, or state changes
// that occurred around the same time as the main event.
func getSameResourceEventEntries(ctx context.Context, db *sqlx.DB, event *models.Event, tenantID string) []TimelineEntry {
	if event.StartsAt == nil || event.CloudAccountId == nil {
		return nil
	}

	// Need at least a resource identifier to match on
	hasResourceId := event.CloudResourceId != nil && *event.CloudResourceId != ""
	hasSubjectName := event.SubjectName != nil && *event.SubjectName != ""
	if !hasResourceId && !hasSubjectName {
		return nil
	}

	// ±60 minute window around the event
	startWindow := event.StartsAt.Add(-60 * time.Minute)
	endWindow := event.StartsAt.Add(60 * time.Minute)

	// Build query based on available identifiers
	// Match by cloud_resource_id (exact resource match) or subject_name + cloud_account_id
	query := `
		SELECT e.id, e.title, e.finding_type, e.source, e.starts_at, e.subject_name
		FROM events e
		WHERE e.tenant = $1
		  AND e.cloud_account_id = $2
		  AND e.id != $3
		  AND e.starts_at >= $4
		  AND e.starts_at <= $5
		  AND NOT EXISTS (
			SELECT 1 FROM event_duplicates ed
			WHERE ed.event_id = e.id AND ed.tenant_id = $1 AND ed.occurrence_number > 1
		  )
		  AND (`
	args := []any{tenantID, *event.CloudAccountId, event.Id, startWindow, endWindow}

	var conditions []string
	if hasResourceId {
		args = append(args, *event.CloudResourceId)
		conditions = append(conditions, fmt.Sprintf("e.cloud_resource_id = $%d", len(args)))
	}
	if hasSubjectName {
		args = append(args, *event.SubjectName)
		conditions = append(conditions, fmt.Sprintf("e.subject_name = $%d", len(args)))
	}
	query += strings.Join(conditions, " OR ")
	query += `)
		ORDER BY e.starts_at ASC
		LIMIT 20`

	var relatedEvents []struct {
		ID          string    `db:"id"`
		Title       string    `db:"title"`
		FindingType *string   `db:"finding_type"`
		Source      *string   `db:"source"`
		StartsAt    time.Time `db:"starts_at"`
		SubjectName *string   `db:"subject_name"`
	}

	err := db.SelectContext(ctx, &relatedEvents, query, args...)
	if err != nil {
		slog.WarnContext(ctx, "Failed to get same-resource events", "error", err, "event_id", event.Id)
		return nil
	}

	var entries []TimelineEntry
	for _, re := range relatedEvents {
		// Skip Anomaly and SLO events
		if re.FindingType != nil {
			ft := strings.ToUpper(*re.FindingType)
			if ft == "ANOMALY" || ft == "SLO" {
				continue
			}
		}

		entries = append(entries, TimelineEntry{
			Timestamp: re.StartsAt,
			RefType:   "event",
			RefID:     re.ID,
			Action:    "related_resource_event",
			Summary:   re.Title,
			Metadata: map[string]interface{}{
				"cloud_account_id": *event.CloudAccountId,
			},
		})
	}
	return entries
}

// extractConfigChangeSummary parses the evidences field to extract meaningful change details
func extractConfigChangeSummary(evidences json.RawMessage, fallbackTitle string) string {
	if len(evidences) == 0 {
		return fallbackTitle
	}

	var evidenceList []struct {
		Type string `json:"type"`
		Data *struct {
			UpdatedPaths  []string `json:"updated_paths"`
			UpdatedValues []struct {
				Path string      `json:"path"`
				Old  interface{} `json:"old"`
				New  interface{} `json:"new"`
			} `json:"updated_values"`
		} `json:"data"`
	}

	if err := json.Unmarshal(evidences, &evidenceList); err != nil {
		slog.Warn("Failed to unmarshal evidences", "error", err)
		return fallbackTitle
	}

	// Look for diff evidence
	var changes []string
	for _, evidence := range evidenceList {
		if evidence.Type != "diff" || evidence.Data == nil {
			continue
		}

		// Extract meaningful changes
		for _, update := range evidence.Data.UpdatedValues {
			path := update.Path
			oldVal := update.Old
			newVal := update.New

			// Extract image changes
			if strings.Contains(path, "image") {
				oldImage := extractImageTag(oldVal)
				newImage := extractImageTag(newVal)
				if oldImage != "" && newImage != "" {
					changes = append(changes, fmt.Sprintf("Image: %s → %s", oldImage, newImage))
				}
			}

			// Extract git commit changes from annotations
			if strings.Contains(path, "ci.nudgebee.com/git.hash") {
				oldCommit := extractStringValue(oldVal)
				newCommit := extractStringValue(newVal)
				if oldCommit != "" && newCommit != "" {
					if len(oldCommit) > 7 {
						oldCommit = oldCommit[:7]
					}
					if len(newCommit) > 7 {
						newCommit = newCommit[:7]
					}
					changes = append(changes, fmt.Sprintf("Git: %s → %s", oldCommit, newCommit))
				}
			}

			// Extract replica changes
			if strings.Contains(path, "replicas") {
				changes = append(changes, fmt.Sprintf("Replicas: %v → %v", oldVal, newVal))
			}
		}
	}

	if len(changes) > 0 {
		return strings.Join(changes, ", ")
	}

	return fallbackTitle
}

// extractImageTag extracts the image tag from an image string or interface
func extractImageTag(value interface{}) string {
	str := extractStringValue(value)
	if str == "" {
		return ""
	}

	// Extract tag from image URL (e.g., "registry.com/image:tag" -> "tag")
	parts := strings.Split(str, ":")
	if len(parts) >= 2 {
		tag := parts[len(parts)-1]
		// Remove digest if present
		if strings.Contains(tag, "@") {
			tag = strings.Split(tag, "@")[0]
		}
		return tag
	}

	return str
}

// extractStringValue converts interface{} to string
func extractStringValue(value interface{}) string {
	if value == nil {
		return ""
	}

	switch v := value.(type) {
	case string:
		return v
	case map[string]interface{}:
		// If it's a nested object, try to get a meaningful string
		if val, ok := v["value"].(string); ok {
			return val
		}
		// Try to marshal it as JSON
		if bytes, err := json.Marshal(v); err == nil {
			return string(bytes)
		}
	}

	return fmt.Sprintf("%v", value)
}
