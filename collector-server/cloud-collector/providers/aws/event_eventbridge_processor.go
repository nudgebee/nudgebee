package aws

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"nudgebee/collector/cloud/common"
	"nudgebee/collector/cloud/providers" // Assuming config is needed for AWS provider instantiation
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"context"
	"database/sql"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	trailtypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	logstypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
	"github.com/aws/smithy-go/ptr"
)

// --- TemplatedEventBridgeProcessor ---

// ErrEventActionResourceMissing indicates that an event-driven action targeted
// a resource that ListResources cannot find. This is typically a benign race
// (resource terminated/deleted between event arrival and action execution) or
// a yet-to-be-indexed new resource — not an error worth alerting on.
var ErrEventActionResourceMissing = errors.New("eventprocessor: action target resource not found")

// awsProviderAPI defines the subset of AWS provider methods needed by actions.
type awsProviderAPI interface {
	QueryLogs(ctx providers.CloudProviderContext, account providers.Account, query providers.QueryLogsRequest) (providers.QueryLogsResponse, error)
	QueryMetrices(ctx providers.CloudProviderContext, account providers.Account, filter providers.QueryMetricsRequest) (providers.QueryMetricsResponse, error)
	ListResources(ctx providers.CloudProviderContext, account providers.Account, query providers.ListResourceRequest) (providers.ListResourcesResponse, error)
	GetECSServiceDetails(ctx providers.CloudProviderContext, account providers.Account, region, clusterIdentifier, serviceIdentifier string) (providers.Resource, error)
	GetECSTaskDefinitionDetails(ctx providers.CloudProviderContext, account providers.Account, region, taskDefinitionIdentifier string) (providers.Resource, error)
	LookupCloudTrailEvents(ctx providers.CloudProviderContext, account providers.Account, region string, input *cloudtrail.LookupEventsInput) ([]trailtypes.Event, error)
}

// TemplatedEventBridgeProcessor processes EventBridgeEvents based on a set of defined rules.
type TemplatedEventBridgeProcessor struct {
	ruleSet     EventRuleSet
	awsAPI      awsProviderAPI
	eligibility *eligibility
	// dedupers holds a debouncer per rule that opted in via dedup_ttl_seconds.
	// Keyed by rule.Name; rules without dedup have no entry. Currently only
	// populated for ActionsOnly rules — see NewTemplatedEventBridgeProcessor.
	dedupers map[string]*ruleDeduper
}

// eligibility is a fast-path filter used by the SQS receive loop to drop
// EventBridge messages that no rule in the configured rule set could ever
// match. Returning false skips per-message DB lookups, AWS API calls and
// template evaluation — we'd have skipped the event at action-execution
// time anyway.
//
// The eligibility set is BUILT DYNAMICALLY from ruleSet.Rules at startup
// (see newEligibility). Each rule contributes a rulePattern derived from
// its triggers.alert_name (source) and event_filters. Patterns we don't
// recognize fall through as "no narrowing" so the rule still matches its
// events at full-process time — we never silently drop because of a
// template shape we couldn't parse.
//
// Eligible() returns true if ANY rulePattern would match the event. If no
// pattern matches, the event is droppable: no rule could fire, no work to
// do.
type eligibility struct {
	rules []rulePattern
}

// rulePattern is the static narrowing extracted from a single rule's
// triggers + event_filters. Empty maps/slices mean "no narrowing extracted"
// for that dimension — the rule treats it as wildcard at fast-skip time.
// Filters whose Go template uses constructs we don't statically parse
// (containsStr, ne, safeIndex, state.value, etc.) simply don't contribute
// — we under-narrow rather than over-narrow.
type rulePattern struct {
	source            string              // from rule.Triggers.Identifier; empty matches any source
	detailTypes       map[string]struct{} // empty = any detail-type
	eventNames        map[string]struct{} // empty + prefixes empty = any eventName
	eventNamePrefixes []string
	lastStatuses      map[string]struct{} // empty = any lastStatus
}

// matches returns true when this rule could fire for the given event based
// only on the dimensions we statically extracted. False positives are
// acceptable (full processing kicks in and decides for real); false
// negatives must not happen — that's a silent drop.
func (rp *rulePattern) matches(ev EventBridgeEvent) bool {
	if rp.source != "" && !strings.EqualFold(rp.source, ev.Source) {
		return false
	}
	if len(rp.detailTypes) > 0 {
		if _, ok := rp.detailTypes[ev.DetailType]; !ok {
			return false
		}
	}
	if len(rp.eventNames) > 0 || len(rp.eventNamePrefixes) > 0 {
		var d struct {
			EventName string `json:"eventName"`
		}
		// If the detail isn't readable, fall through to full processing
		// rather than risk a silent drop.
		if len(ev.Detail) == 0 || json.Unmarshal(ev.Detail, &d) != nil {
			return true
		}
		matched := false
		if _, ok := rp.eventNames[d.EventName]; ok {
			matched = true
		}
		if !matched {
			for _, pfx := range rp.eventNamePrefixes {
				if strings.HasPrefix(d.EventName, pfx) {
					matched = true
					break
				}
			}
		}
		if !matched {
			return false
		}
	}
	if len(rp.lastStatuses) > 0 {
		var d struct {
			LastStatus string `json:"lastStatus"`
		}
		if len(ev.Detail) == 0 || json.Unmarshal(ev.Detail, &d) != nil {
			return true
		}
		if _, ok := rp.lastStatuses[d.LastStatus]; !ok {
			return false
		}
	}
	return true
}

// Filter-template patterns we statically recognize. Anything else (ne,
// containsStr, safeIndex, state.value, eventSource, configuration.*, etc.)
// is intentionally ignored — those filters still gate matching at full-
// process time but don't contribute to fast-skip narrowing.
var (
	reEqDetailTypeDirect = regexp.MustCompile(`eq\s+\(\s*index\s+\.Event\s+"detailType"\s*\)\s+"([^"]+)"`)
	reEqLastStatusDirect = regexp.MustCompile(`eq\s+\(\s*index\s+\.Detail\s+"lastStatus"\s*\)\s+"([^"]+)"`)
	reEqEventNameDirect  = regexp.MustCompile(`eq\s+\(\s*index\s+\.Detail\s+"eventName"\s*\)\s+"([^"]+)"`)
	reVarDecl            = regexp.MustCompile(`\$(\w+)\s*:=\s*\(\s*index\s+\.(Event|Detail)\s+"(\w+)"\s*\)`)
)

// extractRuleNarrows parses a rule's event_filters and returns the static
// narrowing it imposes on (detail-type, eventName, lastStatus). Patterns
// we can't recognize don't add any narrowing — the rule is treated as
// wildcard for the unrecognized dimension at fast-skip time, ensuring
// we never silently drop events the rule would have accepted.
func extractRuleNarrows(filters []EventFilter) rulePattern {
	rp := rulePattern{
		detailTypes:  map[string]struct{}{},
		eventNames:   map[string]struct{}{},
		lastStatuses: map[string]struct{}{},
	}
	for _, f := range filters {
		tpl := f.Template

		// Direct (no var) literal eq matches
		for _, m := range reEqDetailTypeDirect.FindAllStringSubmatch(tpl, -1) {
			rp.detailTypes[m[1]] = struct{}{}
		}
		for _, m := range reEqLastStatusDirect.FindAllStringSubmatch(tpl, -1) {
			rp.lastStatuses[m[1]] = struct{}{}
		}
		for _, m := range reEqEventNameDirect.FindAllStringSubmatch(tpl, -1) {
			rp.eventNames[m[1]] = struct{}{}
		}

		// Variable-bound: e.g. {{ $en := (index .Detail "eventName") }}
		// {{ or (eq $en "X") (eq $en "Y") }}.
		for _, varDecl := range reVarDecl.FindAllStringSubmatch(tpl, -1) {
			varName, scope, field := varDecl[1], varDecl[2], varDecl[3]
			reEqVar := regexp.MustCompile(`eq\s+\$` + regexp.QuoteMeta(varName) + `\s+"([^"]+)"`)
			reHasPrefixVar := regexp.MustCompile(`hasPrefix\s+\$` + regexp.QuoteMeta(varName) + `\s+"([^"]+)"`)
			switch {
			case scope == "Event" && field == "detailType":
				for _, m := range reEqVar.FindAllStringSubmatch(tpl, -1) {
					rp.detailTypes[m[1]] = struct{}{}
				}
			case scope == "Detail" && field == "eventName":
				for _, m := range reEqVar.FindAllStringSubmatch(tpl, -1) {
					rp.eventNames[m[1]] = struct{}{}
				}
				for _, m := range reHasPrefixVar.FindAllStringSubmatch(tpl, -1) {
					rp.eventNamePrefixes = append(rp.eventNamePrefixes, m[1])
				}
			case scope == "Detail" && field == "lastStatus":
				for _, m := range reEqVar.FindAllStringSubmatch(tpl, -1) {
					rp.lastStatuses[m[1]] = struct{}{}
				}
				// Other field bindings (alarmName, configuration.*, etc.)
				// don't contribute to fast-skip — the rule's full filter
				// evaluates them at process time.
			}
		}
	}
	return rp
}

func newEligibility(rs EventRuleSet) *eligibility {
	e := &eligibility{}
	for _, r := range rs.Rules {
		if !strings.EqualFold(r.Triggers.SourceSystem, "AWS_EventBridge") {
			continue
		}
		rp := extractRuleNarrows(r.Triggers.EventFilters)
		rp.source = r.Triggers.Identifier
		e.rules = append(e.rules, rp)
	}
	return e
}

// Eligible reports whether any configured rule could match this event.
// Returning false guarantees no rule would fire, so the receive loop can
// ack-delete without doing any work. False is conservative — if our static
// extraction can't determine a rule's narrowing, the rule is treated as
// wildcard, so events it might accept always reach full processing.
//
// Source==""means we couldn't recognize the body shape (e.g., SNS-wrapped);
// fall through to full processing in that case to preserve correctness.
func (p *TemplatedEventBridgeProcessor) Eligible(ev EventBridgeEvent) bool {
	if p.eligibility == nil || len(p.eligibility.rules) == 0 || ev.Source == "" {
		return true
	}
	for i := range p.eligibility.rules {
		if p.eligibility.rules[i].matches(ev) {
			return true
		}
	}
	return false
}

// GetCloudTrailEventsActionParams defines parameters for the aws_get_cloudtrail_events action.
type GetCloudTrailEventsActionParams struct {
	StartTimeOffset string `json:"start_time_offset,omitempty"` // e.g., "-15m", "-1h"
	EndTimeOffset   string `json:"end_time_offset,omitempty"`   // e.g., "+5m", "0m"
	Region          string `json:"region,omitempty"`            // AWS Region (defaults to event region)
	// LookupAttributes is a list of key-value pairs to filter events.
	// Keys should match cloudtrail.LookupAttributeKey constants (e.g., "EventSource", "EventName").
	LookupAttributes []map[string]string `json:"lookup_attributes,omitempty"` // List of {AttributeKey: "...", AttributeValue: "..."}
	MaxResults       int64               `json:"max_results,omitempty"`       // Max number of events to return (defaults to 100)
}

// UpdateCloudResourceActionParams defines parameters for updating cloud_resourses table
type UpdateCloudResourceActionParams struct {
	ResourceId     string            `json:"resource_id"`
	ServiceName    string            `json:"service_name"`
	ResourceType   string            `json:"resource_type,omitempty"`
	Region         string            `json:"region,omitempty"`
	NewStatus      string            `json:"new_status,omitempty"`
	StatusMapping  map[string]string `json:"status_mapping,omitempty"`
	UpdateLastSeen bool              `json:"update_last_seen,omitempty"`
	UpdateMeta     bool              `json:"update_meta,omitempty"`
	MetaUpdates    map[string]any    `json:"meta_updates,omitempty"`
}

// NewTemplatedEventBridgeProcessor creates a new processor with the given rules.
// Rules with dedup_ttl_seconds > 0 get a deduper, but it stays unbound (no-op)
// until BindContext is called with the long-lived consumer context.
func NewTemplatedEventBridgeProcessor(rules EventRuleSet, api awsProviderAPI) *TemplatedEventBridgeProcessor {
	p := &TemplatedEventBridgeProcessor{ruleSet: rules, awsAPI: api, eligibility: newEligibility(rules)}
	p.dedupers = make(map[string]*ruleDeduper)
	for i := range rules.Rules {
		r := rules.Rules[i]
		if r.DedupTTLSeconds <= 0 {
			continue
		}
		if !r.ActionsOnly {
			// Non-ActionsOnly dedup would also need to coalesce event
			// emissions, which isn't supported yet. Skip silently —
			// rendering an error here would prevent startup; leaving
			// the rule un-deduped degrades gracefully to current
			// behavior.
			continue
		}
		// Capture rule by value so the closure binds to the right one.
		rule := r
		p.dedupers[rule.Name] = newRuleDeduper(
			time.Duration(rule.DedupTTLSeconds)*time.Second,
			func(pCtx providers.CloudProviderContext, ev EventBridgeEvent, acc providers.Account) {
				pCtx.GetLogger().Info("eventprocessor: trailing-fire of deduped rule",
					"ruleName", rule.Name, "eventId", ev.ID)
				p.executeActionsOnlyRule(pCtx, rule, ev, acc, p.prepareTemplateData(ev))
			},
		)
	}
	return p
}

// BindContext binds the long-lived consumer context to all dedupers so
// trailing-fire goroutines can shut down cleanly and the onFire callback
// receives a context whose lifetime matches the consumer's. Call once at
// SQS consumer startup, after construction and before Process. Safe to
// skip in tests that don't exercise the dedup path — Allow degrades to a
// no-op when unbound, so the caller proceeds as if there was no deduper.
func (p *TemplatedEventBridgeProcessor) BindContext(pCtx providers.CloudProviderContext) {
	for _, d := range p.dedupers {
		d.bindContext(pCtx)
	}
}

// executeActionsOnlyRule runs the actions for an ActionsOnly rule. Failures
// are logged and swallowed — actions are best-effort side effects, and any
// emission is suppressed by the ActionsOnly contract.
func (p *TemplatedEventBridgeProcessor) executeActionsOnlyRule(
	ctx providers.CloudProviderContext,
	rule EventProcessingRule,
	ebEvent EventBridgeEvent,
	account providers.Account,
	templateData any,
) {
	logger := ctx.GetLogger().With("ruleName", rule.Name, "eventId", ebEvent.ID, "actionsOnly", true)
	logger.Info("eventprocessor: executing actions-only rule (no event emission)",
		"actionCount", len(rule.Actions))
	for _, actionDef := range rule.Actions {
		if _, err := p.executeAction(ctx, account, ebEvent, actionDef, templateData); err != nil {
			if errors.Is(err, ErrEventActionResourceMissing) {
				logger.Warn("eventprocessor: skipping action in actions-only rule, target resource gone",
					"actionName", actionDef.Name, "actionType", actionDef.Type, "error", err)
			} else {
				logger.Error("eventprocessor: failed to execute action in actions-only rule",
					"actionName", actionDef.Name, "actionType", actionDef.Type, "error", err)
			}
		}
	}
}

// resolveFingerprint renders the rule's Fingerprint template against the
// already-prepared templateData. Returns ("", false) if the rule defines no
// fingerprint, rendering fails, or the rendered value is empty.
func (p *TemplatedEventBridgeProcessor) resolveFingerprint(
	ctx providers.CloudProviderContext,
	rule EventProcessingRule,
	templateData any,
) (string, bool) {
	if rule.EventOutput.Fingerprint.Template == "" && rule.EventOutput.Fingerprint.Value == "" {
		return "", false
	}
	rendered, err := p.renderField(ctx, "Fingerprint", rule.EventOutput.Fingerprint, templateData)
	if err != nil {
		ctx.GetLogger().Warn("eventprocessor: error rendering Fingerprint", "ruleName", rule.Name, "error", err)
		return "", false
	}
	fp := strings.TrimSpace(rendered)
	if fp == "" {
		return "", false
	}
	return fp, true
}

// matches checks if the EventBridgeEvent matches the rule's triggers.
func (p *TemplatedEventBridgeProcessor) matches(ebEvent EventBridgeEvent, trigger EventRuleTrigger, templateDataForFilter any) bool {
	// This processor specifically handles events originating from AWS_EventBridge.
	// Ensure the rule's SourceSystem is intended for EventBridge.
	// Consider using a defined constant for "AWS_EventBridge".
	if !strings.EqualFold(trigger.SourceSystem, "AWS_EventBridge") {
		return false
	}

	// For EventBridge, the rule's 'Identifier' field (alert_name in YAML)
	// should match the EventBridge event's 'source' field.
	if trigger.Identifier != "" && !strings.EqualFold(ebEvent.Source, trigger.Identifier) {
		return false
	}

	// EventBridgeDetailType matching is now handled by EventFilters.
	// Example: an EventFilter with field "detail-type"
	// The templateData for filters will be the same as for event_template and actions.
	// This requires preparing templateData before calling matches, or passing necessary data to matches.
	// For now, let's assume templateData is prepared within Process and passed down or reconstructed here.
	// To avoid re-preparing templateData, Process method should prepare it once.
	// For this diff, we'll pass ebEvent and let matches prepare a minimal context if needed,
	// or ideally, Process prepares full templateData and passes it.
	// Let's assume Process prepares templateData and passes it to matches.
	// (Refactoring Process to prepare templateData once is outside this immediate diff, but a good follow-up)

	// For simplicity in this diff, we'll prepare a temporary templateData for filters here.
	// A more optimized approach would prepare templateData once in the `Process` method.
	// filterTemplateData := p.prepareTemplateData(ebEvent) // Use templateDataForFilter passed as argument

	if len(trigger.EventFilters) > 0 {
		for _, filter := range trigger.EventFilters {
			if !p.evaluateFilterCondition(filter, templateDataForFilter) {
				return false
			}
		}
	}

	return true
}

// evaluateFilterCondition evaluates a single EventFilter's template.
// It expects the template to output "true" or "false".
func (p *TemplatedEventBridgeProcessor) evaluateFilterCondition(filter EventFilter, data any) bool {
	// Note: ctx is not available here. For logging within renderTemplateValue, it would need to be passed.
	// This is a simplification for the diff. Ideally, ctx flows through.
	// For now, renderTemplateValue might need a temporary/nil context or direct logging.
	renderedValue, err := p.renderTemplateValue(nil, "filter:"+filter.Description, filter.Template, data)
	if err != nil {
		slog.Error("eventprocessor: error rendering filter template", "description", filter.Description, "template", filter.Template, "error", err)
		return false // Error in template means filter fails
	}

	switch strings.ToLower(strings.TrimSpace(renderedValue)) {
	case "true", "1":
		return true
	default:
		slog.Debug("eventprocessor: filter condition evaluated to false", "description", filter.Description, "renderedValue", renderedValue)
		return false
	}
}

// renderTemplateValue executes a Go template string with the provided data.
func (p *TemplatedEventBridgeProcessor) renderTemplateValue(ctx providers.CloudProviderContext, fieldName string, templateStr string, data any) (string, error) {
	if templateStr != "" {
		// Register custom template functions
		funcMap := template.FuncMap{
			"splitList": func(delimiter string, s string) []string {
				return strings.Split(s, delimiter)
			},
			"last": func(list []string) string {
				if len(list) == 0 {
					return ""
				}
				return list[len(list)-1]
			},
			"replace": strings.ReplaceAll,
			"toJson": func(v any) (template.HTML, error) {
				b, err := common.MarshalJson(v)
				if err != nil {
					return "", err
				}
				return template.HTML(b), nil // Return as template.HTML to prevent escaping
			},
			"printf": fmt.Sprintf,
			"default": func(defaultValue any, givenValue any) any {
				if givenValue == nil {
					return defaultValue
				}
				val := reflect.ValueOf(givenValue)
				switch val.Kind() {
				case reflect.String:
					if val.String() == "" {
						return defaultValue
					}
				case reflect.Slice, reflect.Array, reflect.Map:
					if val.Len() == 0 {
						return defaultValue
					}
				// Add other kinds like Pointer, Interface and check IsNil if necessary
				case reflect.Pointer, reflect.Interface:
					if val.IsNil() {
						return defaultValue
					}
				}
				return givenValue
			},
			// Comparison and logical functions
			"eq": func(a, b any) bool {
				// Handle common numeric comparisons more gracefully
				// Convert to float64 if both are numbers (int, float, json.Number)
				// This is a simplified example; robust numeric comparison is complex.
				aFloat, aIsNum := anyToFloat64(a)
				bFloat, bIsNum := anyToFloat64(b)
				if aIsNum && bIsNum {
					return aFloat == bFloat
				}
				return reflect.DeepEqual(a, b)
			},
			"ne": func(a, b any) bool {
				aFloat, aIsNum := anyToFloat64(a)
				bFloat, bIsNum := anyToFloat64(b)
				if aIsNum && bIsNum {
					return aFloat != bFloat
				}
				return !reflect.DeepEqual(a, b)
			},
			"isNil": func(a any) bool {
				if a == nil {
					return true
				}
				val := reflect.ValueOf(a)
				switch val.Kind() {
				case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
					return val.IsNil()
				}
				return false
			},
			// String functions
			"containsStr": strings.Contains,
			"hasPrefix":   strings.HasPrefix,
			"hasSuffix":   strings.HasSuffix,
			// Type conversion helpers
			"str": func(v any) string {
				if v == nil {
					return ""
				}
				return fmt.Sprintf("%v", v)
			},
			"toInt": func(v any) (int64, error) {
				num, err := anyToInt64(v)
				if err != nil {
					// Return 0 and error, or handle error display in template differently
					return 0, fmt.Errorf("toInt: %w", err)
				}
				return num, nil
			},
			// CloudTrail specific helpers (optional, but can be useful)
			"cloudtrailLookupAttribute": func(key, value string) map[string]string {
				// Helper to create a map suitable for LookupAttributes in YAML
				return map[string]string{"AttributeKey": key, "AttributeValue": value}
			},
			"cloudtrailLookupAttributeKey": func(key string) string {
				// Helper to get CloudTrail LookupAttributeKey constants
				switch strings.ToLower(key) {
				case "eventsource":
					// return cloudtrail.LookupAttributeKeyEventSource
					return "EventSource"
				case "eventname":
					// return cloudtrail.LookupAttributeKeyEventName
					return "EventName"
				case "resourceid":
					// return cloudtrail.LookupAttributeKeyResourceId
					return "ResourceId"
				case "resourcename":
					// return cloudtrail.LookupAttributeKeyResourceName
					return "ResourceName"
				case "resourcetype": // Added missing case
					// return cloudtrail.LookupAttributeKeyResourceType
					return "ResourceType"
				case "accesskeyid":
					// return cloudtrail.LookupAttributeKeyAccessKeyId
					return "AccessKeyId"
				case "username":
					// return cloudtrail.LookupAttributeKeyUsername
					return "Username"
				case "readOnly":
					// return cloudtrail.LookupAttributeKeyReadOnly
					return "ReadOnly"
				case "eventid":
					// return cloudtrail.LookupAttributeKeyEventId
					return "EventId"
				}
				return key // Return as is if not a known constant
			},
			// safeIndex safely indexes into nested maps/slices, returning empty string on nil/missing values
			// Usage: {{ safeIndex .Detail "userIdentity" "principalId" }}
			"safeIndex": func(obj any, keys ...any) any {
				current := obj
				for _, key := range keys {
					if current == nil {
						return ""
					}

					val := reflect.ValueOf(current)

					// Handle pointers and interfaces by dereferencing
					for val.Kind() == reflect.Pointer || val.Kind() == reflect.Interface {
						if val.IsNil() {
							return ""
						}
						val = val.Elem()
					}

					switch val.Kind() {
					case reflect.Map:
						// Convert key to appropriate type
						var mapKey reflect.Value
						switch k := key.(type) {
						case string:
							mapKey = reflect.ValueOf(k)
						case int:
							mapKey = reflect.ValueOf(k)
						default:
							mapKey = reflect.ValueOf(fmt.Sprintf("%v", k))
						}

						mapVal := val.MapIndex(mapKey)
						if !mapVal.IsValid() {
							return "" // Key doesn't exist
						}
						current = mapVal.Interface()

					case reflect.Slice, reflect.Array:
						// Handle slice/array indexing
						idx, ok := key.(int)
						if !ok {
							// Try to convert to int
							if idxFloat, ok := key.(float64); ok {
								idx = int(idxFloat)
							} else {
								return "" // Invalid index type
							}
						}
						if idx < 0 || idx >= val.Len() {
							return "" // Index out of bounds
						}
						current = val.Index(idx).Interface()

					default:
						// Can't index into this type
						return ""
					}
				}
				return current
			},
			// dimensionsToNameValueJson converts CloudWatch dimensions to a consistent JSON array format.
			// Handles both EventBridge map format {"Key": "Value"} and CloudWatch API array format [{"Name": "Key", "Value": "Value"}].
			"dimensionsToNameValueJson": func(v any) (template.HTML, error) {
				if v == nil {
					return "[]", nil
				}
				// If it's a map (EventBridge format), convert to Name/Value array
				if m, ok := v.(map[string]any); ok {
					result := make([]map[string]string, 0, len(m))
					for name, val := range m {
						result = append(result, map[string]string{"Name": name, "Value": fmt.Sprintf("%v", val)})
					}
					b, err := common.MarshalJson(result)
					if err != nil {
						return "[]", err
					}
					return template.HTML(b), nil
				}
				// Otherwise (already array or other format), serialize as-is
				b, err := common.MarshalJson(v)
				if err != nil {
					return "[]", err
				}
				return template.HTML(b), nil
			},
		}
		tmpl, err := template.New(fieldName).Funcs(funcMap).Parse(templateStr)
		if err != nil {
			if ctx != nil {
				ctx.GetLogger().Error("eventprocessor: failed to parse template", "fieldName", fieldName, "template", templateStr, "error", err)
			}
			return "", fmt.Errorf("eventprocessor: parsing template for %s: %w", fieldName, err)
		}
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, data); err != nil {
			if ctx != nil {
				// Log template data to help debug structure issues
				dataJSON, _ := common.MarshalJson(data)
				ctx.GetLogger().Error("eventprocessor: failed to execute template",
					"fieldName", fieldName,
					"template", templateStr,
					"error", err,
					"eventData", string(dataJSON))
			}
			return "", fmt.Errorf("eventprocessor: executing template for %s: %w", fieldName, err)
		}
		return strings.TrimSpace(buf.String()), nil
	}
	return "", fmt.Errorf("eventprocessor: template string is empty for field %s", fieldName)
}

// renderField uses Go's text/template to render a field from EventFieldTemplate.
func (p *TemplatedEventBridgeProcessor) renderField(ctx providers.CloudProviderContext, fieldName string, fieldTpl EventFieldTemplate, data any) (string, error) {
	if fieldTpl.Template != "" {
		return p.renderTemplateValue(ctx, fieldName, fieldTpl.Template, data)
	}
	return fieldTpl.Value, nil // Return static value if no template
}

// Helper to convert various numeric types to float64 for comparison
func anyToFloat64(v any) (float64, bool) {
	if v == nil {
		return 0, false
	}
	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(val.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(val.Uint()), true
	case reflect.Float32, reflect.Float64:
		return val.Float(), true
	default:
		// Try json.Number
		if jn, ok := v.(json.Number); ok {
			f, err := jn.Float64()
			if err == nil {
				return f, true
			}
		}
		// Try string conversion
		if str, ok := v.(string); ok {
			f, err := strconv.ParseFloat(str, 64)
			if err == nil {
				return f, true
			}
		}
	}
	return 0, false
}

// Helper to convert various types to int64
func anyToInt64(v any) (int64, error) {
	if v == nil {
		return 0, fmt.Errorf("cannot convert nil to int64")
	}
	val := reflect.ValueOf(v)
	switch val.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return val.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64: // Be careful with overflow if uint64 > max_int64
		return int64(val.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return int64(val.Float()), nil // Truncates
	default:
		if jn, ok := v.(json.Number); ok {
			return jn.Int64()
		}
		if str, ok := v.(string); ok {
			return strconv.ParseInt(str, 10, 64)
		}
		return 0, fmt.Errorf("cannot convert %T to int64", v)
	}
}

// renderParamValue recursively renders template values in action parameters.
// It handles strings (as templates), maps (recursively), slices (recursively), and other types (as-is).
func (p *TemplatedEventBridgeProcessor) renderParamValue(
	pCtx providers.CloudProviderContext,
	paramKey string,
	value any,
	templateData any,
) any {
	logger := pCtx.GetLogger()

	switch v := value.(type) {
	case string:
		// Render string as template
		renderedVal, err := p.renderTemplateValue(pCtx, fmt.Sprintf("actionParam_%s", paramKey), v, templateData)
		if err != nil {
			logger.Warn("eventprocessor: failed to render action parameter, using raw value", "paramKey", paramKey, "rawValue", v, "error", err)
			return strings.TrimSpace(v)
		}
		return strings.TrimSpace(renderedVal)

	case map[string]any:
		// Check if this is an EventFieldTemplate pattern (has "template" or "value" key as sole key)
		// If so, render it as a string instead of keeping it as a map
		if templateStr, hasTemplate := v["template"].(string); hasTemplate && len(v) == 1 {
			// Render the template and return as string
			renderedVal, err := p.renderTemplateValue(pCtx, fmt.Sprintf("actionParam_%s", paramKey), templateStr, templateData)
			if err != nil {
				logger.Warn("eventprocessor: failed to render action parameter template, using raw template", "paramKey", paramKey, "template", templateStr, "error", err)
				return strings.TrimSpace(templateStr)
			}
			return strings.TrimSpace(renderedVal)
		}
		if staticValue, hasValue := v["value"]; hasValue && len(v) == 1 {
			// Static value - return as-is (already rendered)
			if valStr, ok := staticValue.(string); ok {
				return strings.TrimSpace(valStr)
			}
			return staticValue
		}
		// Neither template nor value - recursively render map values
		renderedMap := make(map[string]any, len(v))
		for k, val := range v {
			renderedMap[k] = p.renderParamValue(pCtx, fmt.Sprintf("%s.%s", paramKey, k), val, templateData)
		}
		return renderedMap

	case map[any]any:
		// Handle map[any]any (YAML can produce this)
		// First check if it's an EventFieldTemplate pattern (sole key)
		if templateVal, hasTemplate := v["template"]; hasTemplate && len(v) == 1 {
			if templateStr, ok := templateVal.(string); ok {
				renderedVal, err := p.renderTemplateValue(pCtx, fmt.Sprintf("actionParam_%s", paramKey), templateStr, templateData)
				if err != nil {
					logger.Warn("eventprocessor: failed to render action parameter template, using raw template", "paramKey", paramKey, "template", templateStr, "error", err)
					return strings.TrimSpace(templateStr)
				}
				return strings.TrimSpace(renderedVal)
			}
		}
		if staticValue, hasValue := v["value"]; hasValue && len(v) == 1 {
			if valStr, ok := staticValue.(string); ok {
				return strings.TrimSpace(valStr)
			}
			return staticValue
		}
		// Recursively render map values
		renderedMap := make(map[string]any, len(v))
		for k, val := range v {
			keyStr := fmt.Sprintf("%v", k)
			renderedMap[keyStr] = p.renderParamValue(pCtx, fmt.Sprintf("%s.%s", paramKey, keyStr), val, templateData)
		}
		return renderedMap

	case []any:
		// Recursively render slice elements
		renderedSlice := make([]any, len(v))
		for i, val := range v {
			renderedSlice[i] = p.renderParamValue(pCtx, fmt.Sprintf("%s[%d]", paramKey, i), val, templateData)
		}
		return renderedSlice

	default:
		// Return other types as-is (bool, int, float, etc.)
		return v
	}
}

func (p *TemplatedEventBridgeProcessor) executeAction(
	pCtx providers.CloudProviderContext,
	awsAccount providers.Account,
	ebEvent EventBridgeEvent,
	actionDef ActionDefinition,
	templateData any,
) (any, error) {
	logger := pCtx.GetLogger().With("actionName", actionDef.Name, "actionType", actionDef.Type)

	// Render parameters
	renderedParams := make(map[string]any)
	for k, v := range actionDef.Params {
		renderedParams[k] = p.renderParamValue(pCtx, k, v, templateData)
	}

	switch actionDef.Type {
	case "aws_get_resource":
		params := GetResourceActionParams{}
		if err := mapToStruct(renderedParams, &params); err != nil {
			return nil, fmt.Errorf("eventprocessor: parsing aws_get_resource params for action '%s': %w", actionDef.Name, err)
		}
		if params.ServiceName == "" || params.ResourceIdentifier == "" {
			return nil, fmt.Errorf("eventprocessor: aws_get_resource action '%s' requires service_name and resource_identifier", actionDef.Name)
		}
		region := params.Region // Use region from params if provided
		if region == "" {       // Default to event region if not specified in params
			region = ebEvent.Region // Default to event region
		}

		var resource providers.Resource
		var getErr error

		// Attempt specific Get calls first for known types
		if strings.EqualFold(params.ServiceName, "ecs") {
			switch strings.ToLower(params.ResourceType) {
			case "service":
				clusterIdentifier, serviceIdentifier := "", ""
				// If ARN, parse it. If "cluster/service", parse that.
				// If just service name, cluster needs to be inferred or provided.
				// For simplicity, assume resource_identifier is ARN or "clusterName/serviceName"
				if strings.HasPrefix(params.ResourceIdentifier, "arn:aws:ecs:") {
					_, _, _, _, resourcePart := parseARN(params.ResourceIdentifier) // Assuming parseARN is available
					parts := strings.SplitN(resourcePart, "/", 2)
					if len(parts) == 2 {
						clusterIdentifier = parts[0]
						serviceIdentifier = parts[1]
					} else {
						getErr = fmt.Errorf("invalid ECS service ARN format for identifier: %s", params.ResourceIdentifier)
					}
				} else if strings.Contains(params.ResourceIdentifier, "/") { // Assume "cluster/service"
					parts := strings.SplitN(params.ResourceIdentifier, "/", 2)
					if len(parts) == 2 {
						clusterIdentifier = parts[0]
						serviceIdentifier = parts[1]
					} else {
						getErr = fmt.Errorf("invalid cluster/service format for identifier: %s", params.ResourceIdentifier)
					}
				} else {
					// If only service name is provided, we might need cluster from event context or another param.
					// This part needs careful handling based on how identifiers are passed.
					// For now, require ARN or "cluster/service" for direct fetch.
					getErr = fmt.Errorf("ambiguous identifier for ECS service (expected ARN or cluster/service): %s", params.ResourceIdentifier)
				}

				if getErr == nil {
					resource, getErr = p.awsAPI.GetECSServiceDetails(pCtx, awsAccount, region, clusterIdentifier, serviceIdentifier)
				}
			case "task-definition":
				// Assuming resource_identifier is the task definition ARN or family:revision
				resource, getErr = p.awsAPI.GetECSTaskDefinitionDetails(pCtx, awsAccount, region, params.ResourceIdentifier)
			// Add other specific ECS resource types if needed (e.g., "task")
			// case "task":
			// 	// Requires cluster and task identifier. Need to parse from ARN or separate params.
			// 	// resource, getErr = p.awsAPI.GetECSTaskDetails(...)
			default:
				// Fallback to ListResources for other ECS resource types or if type is missing
				logger.Debug("eventprocessor: falling back to ListResources for ECS resource type", "resourceType", params.ResourceType)
				goto fallbackToListResources
			}
		} else {
			// Fallback to ListResources for non-ECS services
			logger.Debug("eventprocessor: falling back to ListResources for non-ECS service", "serviceName", params.ServiceName)
			goto fallbackToListResources
		}

		if getErr != nil {
			// If a specific Get call failed, report the error
			return nil, fmt.Errorf("eventprocessor: failed to get specific resource details for action '%s' (service: %s, type: %s, id: %s): %w",
				actionDef.Name, params.ServiceName, params.ResourceType, params.ResourceIdentifier, getErr)
		}

		// If a specific Get call succeeded, return the result
		return resource, nil

	fallbackToListResources:
		// Fallback to the old ListResources and filter logic
		res, listErr := p.awsAPI.ListResources(pCtx, awsAccount, providers.ListResourceRequest{
			ServiceName: params.ServiceName,
			Regions:     []string{region},
		})
		if listErr != nil {
			return nil, fmt.Errorf("eventprocessor: calling ListResources fallback for action '%s' (service: %s, region: %s): %w", actionDef.Name, params.ServiceName, region, listErr)
		}

		expectedType := getAwsServiceResourceType(params.ServiceName, params.ResourceType)
		for _, r := range res.Items {
			// 1. Filter by ResourceType if provided in action params
			// Also check against the normalized type from serviceResourceTypeMap
			// (e.g., "instance" -> "compute-instance" for EC2)
			if params.ResourceType != "" && !strings.EqualFold(r.Type, params.ResourceType) && !strings.EqualFold(r.Type, expectedType) {
				continue // Skip if resource type doesn't match even after normalization
			}

			// 2. Filter by Identifier based on IdentifierType
			matched := false
			switch strings.ToLower(params.IdentifierType) {
			case "id":
				matched = (r.Id == params.ResourceIdentifier)
			case "name":
				matched = (r.Name == params.ResourceIdentifier)
			case "arn":
				matched = (r.Arn == params.ResourceIdentifier)
			default: // Default behavior (if IdentifierType is empty or unrecognized): check ID and ARN
				matched = (r.Id == params.ResourceIdentifier || r.Arn == params.ResourceIdentifier)
			}

			if matched {
				return r, nil // Return the first match
			}
		}
		return nil, fmt.Errorf("%w: resource '%s' (type: '%s', idType: '%s') for action '%s' (service: %s, region: %s)",
			ErrEventActionResourceMissing, params.ResourceIdentifier, params.ResourceType, params.IdentifierType, actionDef.Name, params.ServiceName, region)

	case "aws_get_metric":
		params := GetMetricActionParams{}
		if err := mapToStruct(renderedParams, &params); err != nil {
			return nil, fmt.Errorf("eventprocessor: parsing aws_get_metric params: %w", err)
		}

		// When dimensions or statistic are missing/default, auto-extract from the alarm's
		// metric configuration. CloudWatch GetMetricData requires dimensions to scope the
		// query; without them, the API returns empty results for most alarms.
		if len(params.Dimensions) == 0 || params.Statistic == "Average" || params.Statistic == "" {
			dims, stat := extractAlarmMetricDetails(ebEvent)
			if len(params.Dimensions) == 0 && len(dims) > 0 {
				params.Dimensions = dims
			}
			if (params.Statistic == "Average" || params.Statistic == "") && stat != "" {
				params.Statistic = stat
			}
		}

		startTime, endTime, err := parseTimeOffsets(ebEvent.Time, params.StartTimeOffset, params.EndTimeOffset)
		if err != nil {
			return nil, fmt.Errorf("eventprocessor: parsing time offsets for aws_get_metric: %w", err)
		}
		metricQuery := providers.QueryMetricsRequest{
			ServiceName:     params.Namespace,
			MetricNamespace: params.Namespace, // Pass CW namespace directly so getAwsCloudwatchMetrics uses it as-is
			MetricNames:     []string{params.MetricName},
			Dimensions:      params.Dimensions,
			Step:            time.Duration(params.PeriodSeconds) * time.Second,
			Statistics:      []string{params.Statistic},
			StartDate:       &startTime,
			EndDate:         &endTime,
			Region:          ebEvent.Region,
		}
		return p.awsAPI.QueryMetrices(pCtx, awsAccount, metricQuery)

	case "aws_get_log":
		params := GetLogActionParams{}
		if err := mapToStruct(renderedParams, &params); err != nil {
			return nil, fmt.Errorf("eventprocessor: parsing aws_get_log params: %w", err)
		}
		startTime, endTime, err := parseTimeOffsets(ebEvent.Time, params.StartTimeOffset, params.EndTimeOffset)
		if err != nil {
			return nil, fmt.Errorf("eventprocessor: parsing time offsets for aws_get_log: %w", err)
		}
		actualLogGroupName := params.LogGroupName
		if params.AutoDetectLogGroup {
			// If auto-discovery is requested, LogGroupName parameter is ignored (or can be empty).
			// ClusterName and ServiceName are required for auto-discovery.
			if params.ClusterName == "" || params.ServiceName == "" {
				return nil, fmt.Errorf("eventprocessor: aws_get_log action '%s' with auto_detect_log_group=true requires cluster_name and service_name parameters", actionDef.Name)
			}
			logger.Info("eventprocessor: auto-discovering log group for ECS service", "cluster", params.ClusterName, "service", params.ServiceName)

			// 1. Get Service Details to find the Task Definition ARN
			serviceDetails, errService := p.awsAPI.GetECSServiceDetails(pCtx, awsAccount, ebEvent.Region, params.ClusterName, params.ServiceName)
			if errService != nil {
				return nil, fmt.Errorf("eventprocessor: failed to get ECS service details for log group auto-discovery (action: %s): %w", actionDef.Name, errService)
			}
			taskDefArn, _ := serviceDetails.Meta["TaskDefinition"].(string) // Assuming taskDefinition ARN is stored here
			if taskDefArn == "" {
				return nil, fmt.Errorf("eventprocessor: could not find taskDefinition ARN in service details for log group auto-discovery (service: %s, action: %s)", params.ServiceName, actionDef.Name)
			}

			// 2. Get Task Definition Details to find the log configuration
			taskDefDetails, errTd := p.awsAPI.GetECSTaskDefinitionDetails(pCtx, awsAccount, ebEvent.Region, taskDefArn)
			if errTd != nil {
				return nil, fmt.Errorf("eventprocessor: failed to get ECS task definition details for log group auto-discovery (action: %s): %w", actionDef.Name, errTd)
			}
			actualLogGroupName = parseLogGroupFromTaskDefMeta(taskDefDetails.Meta) // Use your existing helper
			if actualLogGroupName == "" {
				return nil, fmt.Errorf("eventprocessor: failed to auto-discover log group name from task definition '%s' for service '%s' (action: %s)", taskDefArn, params.ServiceName, actionDef.Name)
			}
			logger.Info("eventprocessor: auto-discovered log group name", "logGroupName", actualLogGroupName, "service", params.ServiceName)
		} else if actualLogGroupName == "" {
			// If auto-discovery is NOT requested and LogGroupName is empty, it's an error.
			return nil, fmt.Errorf("eventprocessor: aws_get_log action '%s' requires either a non-empty log_group_name or auto_detect_log_group=true (with cluster_name and service_name)", actionDef.Name)
		}
		limit := params.Limit
		if limit == 0 {
			limit = 20 // Default limit
		}
		logQuery := providers.QueryLogsRequest{
			LogGroupName: actualLogGroupName, // Use the discovered or provided name
			QueryString:  params.Query,
			StartTime:    &startTime,
			EndTime:      &endTime,
			Limit:        aws.Int64(limit),
			Region:       ebEvent.Region, // Default to event region
		}
		return p.awsAPI.QueryLogs(pCtx, awsAccount, logQuery)

	case "aws_create_alarm":
		cfg, err := getAwsConfigFromAccount(context.TODO(), awsAccount)
		if err != nil {
			return nil, fmt.Errorf("eventprocessor: failed to get AWS config for region %s: %w", ebEvent.Region, err)
		}
		cfg.Region = ebEvent.Region
		cwClient := cloudwatch.NewFromConfig(cfg)
		logsClient := cloudwatchlogs.NewFromConfig(cfg)

		alarmType, _ := renderedParams["alarm_type"].(string)
		if alarmType == "" {
			return nil, fmt.Errorf("eventprocessor: aws_create_alarm action '%s' requires 'alarm_type' parameter", actionDef.Name)
		}

		// Helper to get nested values from templateData using dot-notation path
		// This is a simplified version. A robust one would handle errors, types, etc.
		// You might already have a more robust `navigatePath` or similar helper.
		getTemplateDataValue := func(path string) any {
			keys := strings.Split(path, ".")
			var current = templateData
			for _, key := range keys {
				if current == nil { // Path became invalid in a previous step
					return nil
				}
				if m, ok := current.(map[string]any); ok {
					val, found := m[key]
					if !found {
						return nil // Key not found in map
					}
					current = val
				} else if s, ok := current.(struct {
					Event  map[string]any
					Detail map[string]any
				}); ok { // Specific handling for top-level templateData struct
					switch key {
					case "Event":
						current = s.Event
					case "Detail":
						current = s.Detail
					default:
						// Accessing a field not named Event or Detail on the root templateData struct
						// This case should ideally not happen if paths are well-defined.
						// For robustness, one might use reflection here if arbitrary struct field access was needed.
						// However, given the known structure, direct field access is fine if paths are "Event.something" or "Detail.something".
						// If the path is just "Event" or "Detail", it's handled above.
						// If the path is something else at the root, it's an invalid path for this specific struct.
						return nil
					}
				} else {
					return nil // Current item is not a map and not the root templateData struct, cannot navigate further with dot notation.
				}
			}
			return current
		}

		clusterNamePath, _ := renderedParams["cluster_name_path"].(string)
		serviceNamePath, _ := renderedParams["service_name_path"].(string)

		var rawClusterArn, serviceName string

		if rawClusterArnVal := getTemplateDataValue(clusterNamePath); rawClusterArnVal != nil {
			rawClusterArn, _ = rawClusterArnVal.(string)
		} else {
			logger.Warn("eventprocessor: cluster_name_path did not resolve to a value", "path", clusterNamePath, "alarmType", alarmType)
		}

		if serviceNameVal := getTemplateDataValue(serviceNamePath); serviceNameVal != nil {
			serviceName, _ = serviceNameVal.(string)
		} else {
			logger.Warn("eventprocessor: service_name_path did not resolve to a value", "path", serviceNamePath, "alarmType", alarmType)
		}

		if rawClusterArn == "" || serviceName == "" {
			return nil, fmt.Errorf("eventprocessor: could not extract cluster_name or service_name for alarm type '%s' using paths '%s', '%s'", alarmType, clusterNamePath, serviceNamePath)
		}

		// Extract short cluster name from ARN
		clusterNameParts := strings.Split(rawClusterArn, "/")
		clusterName := rawClusterArn
		if len(clusterNameParts) > 1 {
			clusterName = clusterNameParts[len(clusterNameParts)-1]
		}

		defaultThreshold := 80.0 // Default threshold
		if t, ok := renderedParams["threshold"].(float64); ok {
			defaultThreshold = t
		} else if tStr, ok := renderedParams["threshold"].(string); ok {
			if tFloat, err := strconv.ParseFloat(tStr, 64); err == nil {
				defaultThreshold = tFloat
			}
		}

		defaultEvaluationPeriods := int64(2)
		if ep, ok := renderedParams["evaluation_periods"].(int64); ok && ep > 0 {
			defaultEvaluationPeriods = ep
		} else if epStr, ok := renderedParams["evaluation_periods"].(string); ok {
			if epInt, err := strconv.ParseInt(epStr, 10, 64); err == nil && epInt > 0 {
				defaultEvaluationPeriods = epInt
			}
		}

		commonTags := []types.Tag{
			{
				Key:   ptr.String("created-by"),
				Value: ptr.String("NudgebeeCollector"),
			},
		}

		alarmInputBase := cloudwatch.PutMetricAlarmInput{
			ActionsEnabled:     ptr.Bool(true), // Sends to EventBridge by default if no AlarmActions
			Period:             ptr.Int32(300),
			EvaluationPeriods:  ptr.Int32(int32(defaultEvaluationPeriods)),
			ComparisonOperator: types.ComparisonOperatorGreaterThanThreshold,
			TreatMissingData:   ptr.String("breaching"), // Sensible default for utilization
			Dimensions: []types.Dimension{
				{Name: ptr.String("ClusterName"), Value: ptr.String(clusterName)},
				{Name: ptr.String("ServiceName"), Value: ptr.String(serviceName)},
			},
			Tags: commonTags, // Add common tags to all alarms
		}

		switch alarmType {
		case "ecs_cpu_utilization":
			alarmInput := alarmInputBase
			alarmInput.AlarmName = ptr.String(fmt.Sprintf("%s-%s-HighCPUUtilization-Auto", clusterName, serviceName))
			alarmInput.AlarmDescription = ptr.String(fmt.Sprintf("Auto-created: CPU utilization for %s in %s > %.0f%%", serviceName, clusterName, defaultThreshold))
			alarmInput.MetricName = ptr.String("CPUUtilization")
			alarmInput.Namespace = ptr.String("AWS/ECS")
			alarmInput.Statistic = types.StatisticAverage
			alarmInput.Threshold = ptr.Float64(defaultThreshold)

			_, err := cwClient.PutMetricAlarm(context.TODO(), &alarmInput)
			if err != nil {
				return nil, fmt.Errorf("eventprocessor: failed to create CPU alarm for %s: %w", serviceName, err)
			}
			return fmt.Sprintf("CPU alarm created for %s/%s", clusterName, serviceName), nil

		case "ecs_memory_utilization":
			alarmInput := alarmInputBase
			alarmInput.AlarmName = ptr.String(fmt.Sprintf("%s-%s-HighMemoryUtilization-Auto", clusterName, serviceName))
			alarmInput.AlarmDescription = ptr.String(fmt.Sprintf("Auto-created: Memory utilization for %s in %s > %.0f%%", serviceName, clusterName, defaultThreshold))
			alarmInput.MetricName = ptr.String("MemoryUtilization")
			alarmInput.Namespace = ptr.String("AWS/ECS")
			alarmInput.Statistic = types.StatisticAverage
			alarmInput.Threshold = ptr.Float64(defaultThreshold)

			_, err := cwClient.PutMetricAlarm(context.TODO(), &alarmInput)
			if err != nil {
				return nil, fmt.Errorf("eventprocessor: failed to create Memory alarm for %s: %w", serviceName, err)
			}
			return fmt.Sprintf("Memory alarm created for %s/%s", clusterName, serviceName), nil

		case "ecs_log_errors":
			taskDefArnPath, _ := renderedParams["task_definition_arn_path"].(string)
			taskDefArn, _ := getTemplateDataValue(taskDefArnPath).(string)
			if taskDefArn == "" {
				return nil, fmt.Errorf("eventprocessor: 'task_definition_arn_path' yielding empty ARN for ecs_log_errors alarm")
			}

			// Determine log group name:
			// 1. Check if log_group_name is explicitly provided in YAML params.
			// 2. If not, attempt to parse it from the task definition's log configuration.
			// 3. If still not found, fall back to a convention based on service name.
			logGroupName := ""
			if lgn, ok := renderedParams["log_group_name"].(string); ok && lgn != "" {
				logGroupName = lgn // Use explicit log group name from YAML
			} else {
				// Attempt to get log group from task definition
				taskDefResource, err := p.awsAPI.GetECSTaskDefinitionDetails(pCtx, awsAccount, ebEvent.Region, taskDefArn)
				if err != nil {
					logger.Warn("eventprocessor: failed to get task definition details to determine log group, falling back to convention", "taskDefArn", taskDefArn, "error", err)
				} else if taskDefResource.Meta != nil {
					if parsedLogGroup := parseLogGroupFromTaskDefMeta(taskDefResource.Meta); parsedLogGroup != "" {
						logGroupName = parsedLogGroup
					}
				}
			}
			// Fallback to convention if still not determined
			if logGroupName == "" {
				logGroupName = fmt.Sprintf("/ecs/%s", serviceName) // ADJUST THIS CONVENTION
			}

			if lgn, ok := renderedParams["log_group_name"].(string); ok && lgn != "" {
				logGroupName = lgn
			}

			filterPattern := "?error ?Error ?exception ?Exception ?EXCEPTION ?Warning"
			if fp, ok := renderedParams["filter_pattern"].(string); ok && fp != "" {
				filterPattern = fp
			}

			if logGroupName == "" {
				// If log group name could not be determined by any method, we cannot create the filter/alarm.
				return nil, fmt.Errorf("eventprocessor: could not determine log group name for service '%s' from task definition '%s' or params", serviceName, taskDefArn)
			}

			metricNamespace := fmt.Sprintf("ECS/%s/Logs-Auto", serviceName)
			metricName := "ApplicationLogErrors-Auto"
			filterName := fmt.Sprintf("%s-LogErrorFilter-Auto", serviceName)

			// Attempt to create the log group. This is idempotent.
			// If it already exists, no error. If it doesn't, it's created.
			// This helps prevent "log group does not exist" errors for PutMetricFilter.
			_, err := logsClient.CreateLogGroup(context.TODO(), &cloudwatchlogs.CreateLogGroupInput{
				LogGroupName: ptr.String(logGroupName),
				Tags: map[string]string{ // Add tags to the log group
					"created-by": "NudgebeeCollector",
				},
				// Optionally, add KMS key ID here if needed for log groups
			})
			if err != nil {
				if !strings.Contains(err.Error(), "ResourceAlreadyExistsException") {
					// For other errors, log a warning but attempt to proceed with PutMetricFilter.
					logger.Warn("eventprocessor: failed to ensure log group exists, PutMetricFilter might fail", "logGroupName", logGroupName, "error", err)
				}
			}

			_, err = logsClient.PutMetricFilter(context.TODO(), &cloudwatchlogs.PutMetricFilterInput{
				LogGroupName:  ptr.String(logGroupName),
				FilterName:    ptr.String(filterName),
				FilterPattern: ptr.String(filterPattern),
				MetricTransformations: []logstypes.MetricTransformation{
					{
						MetricName:      ptr.String(metricName),
						MetricNamespace: ptr.String(metricNamespace),
						MetricValue:     ptr.String("1"),
						DefaultValue:    ptr.Float64(0),
					},
				},
			})
			// Idempotency: If filter already exists with same name and pattern, this is fine.
			// If it exists with a different pattern under the same name, PutMetricFilter will error.
			// Consider DeleteMetricFilter first if updates to existing filters are intended.
			if err != nil {
				if !strings.Contains(err.Error(), "ResourceAlreadyExistsException") {
					return nil, fmt.Errorf("eventprocessor: failed to create/update log metric filter for %s: %w", serviceName, err)
				}
			}

			_, err = cwClient.PutMetricAlarm(context.TODO(), &cloudwatch.PutMetricAlarmInput{
				AlarmName:          ptr.String(fmt.Sprintf("%s-%s-TaskLogErrors-Auto", clusterName, serviceName)),
				AlarmDescription:   ptr.String(fmt.Sprintf("Auto-created: Errors/Warnings in logs for %s", serviceName)),
				ActionsEnabled:     ptr.Bool(true),
				MetricName:         ptr.String(metricName),
				Namespace:          ptr.String(metricNamespace),
				Statistic:          types.StatisticSum,
				Period:             ptr.Int32(300),
				EvaluationPeriods:  ptr.Int32(1),
				Threshold:          ptr.Float64(0),
				ComparisonOperator: types.ComparisonOperatorGreaterThanThreshold,
				TreatMissingData:   ptr.String("notBreaching"),
			})
			if err != nil {
				return nil, fmt.Errorf("eventprocessor: failed to create log error alarm for %s: %w", serviceName, err)
			}
			return fmt.Sprintf("Log error alarm and filter created for %s/%s", clusterName, serviceName), nil

		case "ecs_pending_count_too_long":
			alarmInput := alarmInputBase // Start with base, then customize
			alarmInput.AlarmName = ptr.String(fmt.Sprintf("%s-%s-PendingTasksTooLong-Auto", clusterName, serviceName))
			alarmInput.AlarmDescription = ptr.String(fmt.Sprintf("Auto-created: ECS service %s in %s has tasks pending for too long", serviceName, clusterName))
			alarmInput.MetricName = ptr.String("PendingCount")
			alarmInput.Namespace = ptr.String("AWS/ECS")
			alarmInput.Statistic = types.StatisticMaximum // Alarm if max pending count over period is > 0
			alarmInput.Threshold = ptr.Float64(0)         // Threshold is > 0 pending tasks
			alarmInput.ComparisonOperator = types.ComparisonOperatorGreaterThanThreshold
			// EvaluationPeriods might be longer for "too long", e.g., 3 periods of 5 minutes = 15 minutes
			// This can be overridden by "evaluation_periods" in YAML params.
			if _, ok := renderedParams["evaluation_periods"]; !ok { // If not overridden, use a specific default for this alarm type
				alarmInput.EvaluationPeriods = ptr.Int32(3) // e.g., 3 * 5min = 15min
			}
			alarmInput.TreatMissingData = ptr.String("notBreaching") // If no pending tasks, it's not an issue

			_, err := cwClient.PutMetricAlarm(context.TODO(), &alarmInput)
			if err != nil {
				return nil, fmt.Errorf("eventprocessor: failed to create PendingTasksTooLong alarm for %s: %w", serviceName, err)
			}
			return fmt.Sprintf("PendingTasksTooLong alarm created for %s/%s", clusterName, serviceName), nil

		case "ecs_running_count_low":
			desiredCountPath, _ := renderedParams["desired_count_path"].(string)
			desiredCountVal := getTemplateDataValue(desiredCountPath)
			desiredCount, convErr := anyToInt64(desiredCountVal) // Use your existing anyToInt64 helper

			if desiredCountPath == "" || convErr != nil || desiredCount <= 0 {
				return nil, fmt.Errorf("eventprocessor: 'desired_count_path' yielding invalid desiredCount ('%v', err: %v) for ecs_running_count_low alarm", desiredCountVal, convErr)
			}

			alarmInput := alarmInputBase // Start with base, then customize
			alarmInput.AlarmName = ptr.String(fmt.Sprintf("%s-%s-RunningCountLow-Auto", clusterName, serviceName))
			alarmInput.AlarmDescription = ptr.String(fmt.Sprintf("Auto-created: ECS service %s in %s has running count below desired (%d)", serviceName, clusterName, desiredCount))
			alarmInput.MetricName = ptr.String("RunningCount")
			alarmInput.Namespace = ptr.String("AWS/ECS")
			alarmInput.Statistic = types.StatisticMinimum // Alarm if min running count over period is < desired
			alarmInput.Threshold = ptr.Float64(float64(desiredCount))
			alarmInput.ComparisonOperator = types.ComparisonOperatorLessThanThreshold
			// TreatMissingData: breaching (if metric is missing, assume tasks are not running)

			_, err := cwClient.PutMetricAlarm(context.TODO(), &alarmInput)
			if err != nil {
				return nil, fmt.Errorf("eventprocessor: failed to create RunningCountLow alarm for %s: %w", serviceName, err)
			}
			return fmt.Sprintf("RunningCountLow alarm created for %s/%s (desired: %d)", clusterName, serviceName, desiredCount), nil

		case "aws_get_cloudtrail_events":
			paramsCT := GetCloudTrailEventsActionParams{}
			if err := mapToStruct(renderedParams, &paramsCT); err != nil {
				return nil, fmt.Errorf("eventprocessor: parsing aws_get_cloudtrail_events params for action '%s': %w", actionDef.Name, err)
			}

			region := paramsCT.Region
			if region == "" {
				region = ebEvent.Region // Default to event region
			}

			startTimeCT, endTimeCT, err := parseTimeOffsets(ebEvent.Time, paramsCT.StartTimeOffset, paramsCT.EndTimeOffset)
			if err != nil {
				return nil, fmt.Errorf("eventprocessor: parsing time offsets for aws_get_cloudtrail_events: %w", err)
			}

			maxResults := paramsCT.MaxResults
			if maxResults == 0 {
				maxResults = 100 // Default limit for CloudTrail lookup
			}

			lookupAttributes := []trailtypes.LookupAttribute{}
			for _, attrMap := range paramsCT.LookupAttributes {
				key, keyOk := attrMap["AttributeKey"]
				value, valueOk := attrMap["AttributeValue"]
				if keyOk && valueOk && key != "" {
					lookupAttributes = append(lookupAttributes, trailtypes.LookupAttribute{
						AttributeKey:   trailtypes.LookupAttributeKey(key),
						AttributeValue: ptr.String(value),
					})
				} else {
					logger.Warn("eventprocessor: skipping invalid lookup_attribute in params for aws_get_cloudtrail_events", "attribute", attrMap)
				}
			}

			// Default to ReadOnly=false unless explicitly overridden
			readOnlyFilterExists := false
			for _, attr := range lookupAttributes {
				if attr.AttributeKey == trailtypes.LookupAttributeKeyReadOnly { // Using string literal as per previous fix
					readOnlyFilterExists = true
					break
				}
			}
			if !readOnlyFilterExists {
				lookupAttributes = append(lookupAttributes, trailtypes.LookupAttribute{AttributeKey: trailtypes.LookupAttributeKeyReadOnly, AttributeValue: ptr.String("false")})
			}

			cloudtrailEvents, err := p.awsAPI.LookupCloudTrailEvents(pCtx, awsAccount, region, &cloudtrail.LookupEventsInput{
				StartTime:        ptr.Time(startTimeCT),
				EndTime:          ptr.Time(endTimeCT),
				LookupAttributes: lookupAttributes,
				MaxResults:       ptr.Int32(int32(maxResults)),
			})
			if err != nil {
				return nil, fmt.Errorf("eventprocessor: failed to lookup cloudtrail events for action '%s': %w", actionDef.Name, err)
			}
			return cloudtrailEvents, nil

		default:
			return nil, fmt.Errorf("eventprocessor: unsupported alarm_type '%s' for aws_create_alarm action", alarmType)
		}

	case "update_cloud_resource":
		logger.Info("eventprocessor: executing update_cloud_resource action", "actionName", actionDef.Name, "renderedParams", renderedParams)
		params := UpdateCloudResourceActionParams{}
		if err := mapToStruct(renderedParams, &params); err != nil {
			return nil, fmt.Errorf("eventprocessor: parsing update_cloud_resource params for action '%s': %w", actionDef.Name, err)
		}

		logger.Info("eventprocessor: parsed update_cloud_resource params", "params", params)
		return p.updateCloudResource(pCtx, awsAccount, ebEvent, params)

	default:
		return nil, fmt.Errorf("eventprocessor: unsupported action type: %s", actionDef.Type)
	}
}

// updateCloudResource updates a resource in the cloud_resourses table based on EventBridge event
func (p *TemplatedEventBridgeProcessor) updateCloudResource(
	pCtx providers.CloudProviderContext,
	awsAccount providers.Account,
	ebEvent EventBridgeEvent,
	params UpdateCloudResourceActionParams,
) (any, error) {
	logger := pCtx.GetLogger().With("action", "update_cloud_resource", "resourceId", params.ResourceId)
	logger.Info("eventprocessor: updateCloudResource called",
		"resourceId", params.ResourceId,
		"serviceName", params.ServiceName,
		"resourceType", params.ResourceType,
		"region", params.Region,
		"newStatus", params.NewStatus,
		"accountNumber", awsAccount.AccountNumber)

	// Get account metadata (UUID) from cache
	accountID, tenantID, found := GetAccountMetadata(awsAccount.AccountNumber)
	if !found {
		logger.Error("eventprocessor: account metadata not found in cache", "accountNumber", awsAccount.AccountNumber)
		return nil, fmt.Errorf("eventprocessor: account metadata not found in cache for account %s", awsAccount.AccountNumber)
	}
	logger.Info("eventprocessor: found account metadata", "accountID", accountID, "tenantID", tenantID)

	// Get database manager
	dbms, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		logger.Error("eventprocessor: unable to get database manager", "error", err)
		return nil, fmt.Errorf("eventprocessor: failed to get database manager: %w", err)
	}

	// Determine final status - apply status mapping if provided
	finalStatus := params.NewStatus
	if len(params.StatusMapping) > 0 && params.NewStatus != "" {
		if mappedStatus, ok := params.StatusMapping[params.NewStatus]; ok {
			finalStatus = mappedStatus
		}
	}

	// Fetch resource details BEFORE UPSERT to get ARN, name, type, tags, and meta
	// This prevents creating duplicate entries with wrong external_resource_id
	var resourceArn string
	var resourceName string
	var resourceType string
	var resourceTags = "{}"
	var resourceMeta = "{}"

	cloudProvider, ok := providers.GetProvider("AWS")
	if ok {
		resourceResp, err := cloudProvider.ListResources(pCtx, awsAccount, providers.ListResourceRequest{
			ServiceName: params.ServiceName,
			Regions:     []string{params.Region},
			ResourceIds: []string{params.ResourceId},
		})
		if err != nil {
			logger.Warn("eventprocessor: failed to fetch resource details before upsert, will query existing record", "error", err)
			// Query existing record to get the correct type (to avoid unique constraint violation)
			// The type from params might differ from what's in DB (e.g., "instance" vs "compute-instance")
			var existingType sql.NullString
			queryExisting := `SELECT type FROM cloud_resourses WHERE account = $1 AND resourse_id = $2 AND service_name = $3 AND region = $4 LIMIT 1`
			row, queryErr := dbms.QueryRow(queryExisting, accountID, params.ResourceId, params.ServiceName, params.Region)
			if queryErr == nil {
				if scanErr := row.Scan(&existingType); scanErr == nil && existingType.Valid {
					resourceType = existingType.String
					logger.Info("eventprocessor: found existing resource type from database", "type", resourceType)
				} else {
					// No existing record or scan failed, use params
					resourceType = params.ResourceType
					if scanErr != nil {
						logger.Info("eventprocessor: no existing record found or scan failed, using params type", "scanError", scanErr, "type", resourceType)
					} else {
						logger.Info("eventprocessor: existing type is NULL, using params type", "type", resourceType)
					}
				}
			} else {
				// Query failed, use params
				resourceType = params.ResourceType
				logger.Warn("eventprocessor: failed to query existing record, using params type", "queryError", queryErr, "type", resourceType)
			}
			// Use resourceId as fallback external_resource_id
			resourceArn = params.ResourceId
		} else if len(resourceResp.Items) > 0 {
			resource := resourceResp.Items[0]
			logger.Info("eventprocessor: fetched resource details before upsert",
				"resourceId", resource.Id,
				"arn", resource.Arn,
				"type", resource.Type)

			resourceArn = resource.Arn
			resourceName = resource.Name
			resourceType = resource.Type // Use the actual type from AWS, not from params

			if len(resource.Tags) > 0 {
				tagsJsonBytes, err := common.MarshalJson(resource.Tags)
				if err == nil {
					resourceTags = string(tagsJsonBytes)
				} else {
					logger.Error("eventprocessor: failed to marshal resource tags", "error", err)
				}
			}

			if len(resource.Meta) > 0 {
				metaJsonBytes, err := common.MarshalJson(resource.Meta)
				if err == nil {
					resourceMeta = string(metaJsonBytes)
				} else {
					logger.Error("eventprocessor: failed to marshal resource meta", "error", err)
				}
			}
		} else {
			logger.Warn("eventprocessor: no resource found for resourceId, will use params only", "resourceId", params.ResourceId)
			resourceArn = params.ResourceId
			resourceType = params.ResourceType
		}
	} else {
		logger.Warn("eventprocessor: AWS provider not found, will use params only")
		resourceArn = params.ResourceId
		resourceType = params.ResourceType
	}

	// If we still don't have an ARN, use the resourceId as fallback
	if resourceArn == "" {
		resourceArn = params.ResourceId
	}
	if resourceType == "" {
		resourceType = params.ResourceType
	}

	// Build UPSERT query (INSERT...ON CONFLICT...DO UPDATE)
	args := []any{}
	argIndex := 1
	now := time.Now().UTC().Format(time.RFC3339)

	// Prepare meta JSON if meta updates are provided from params
	// This will be merged with the resource meta fetched above
	var paramMetaJson string
	if params.UpdateMeta && len(params.MetaUpdates) > 0 {
		metaJsonBytes, err := common.MarshalJson(params.MetaUpdates)
		if err != nil {
			logger.Error("eventprocessor: failed to marshal meta updates", "error", err)
			return nil, fmt.Errorf("eventprocessor: failed to marshal meta updates: %w", err)
		}
		paramMetaJson = string(metaJsonBytes)
	}

	// Merge fetched resource meta with param meta updates
	finalMetaJson := resourceMeta
	if paramMetaJson != "" {
		if resourceMeta != "" && resourceMeta != "{}" {
			// Merge both meta objects
			var fetchedMeta, paramMeta map[string]any
			if err := common.UnmarshalJson([]byte(resourceMeta), &fetchedMeta); err == nil {
				if err := common.UnmarshalJson([]byte(paramMetaJson), &paramMeta); err == nil {
					// Merge param updates into fetched meta
					for k, v := range paramMeta {
						fetchedMeta[k] = v
					}
					mergedBytes, err := common.MarshalJson(fetchedMeta)
					if err == nil {
						finalMetaJson = string(mergedBytes)
					} else {
						logger.Error("eventprocessor: failed to marshal merged meta", "error", err)
					}
				}
			}
		} else {
			// No fetched meta, just use param meta
			finalMetaJson = paramMetaJson
		}
	}

	// INSERT clause - prepare values for new resource including external_resource_id
	insertColumns := []string{
		"tenant", "account", "cloud_provider", "resourse_id", "external_resource_id",
		"service_name", "region", "type", "name", "arn", "tags", "meta",
		"created_at", "updated_at", "first_seen", "last_seen",
	}
	insertPlaceholders := []string{
		fmt.Sprintf("$%d", argIndex),           // tenant
		fmt.Sprintf("$%d", argIndex+1),         // account
		fmt.Sprintf("$%d", argIndex+2),         // cloud_provider
		fmt.Sprintf("$%d", argIndex+3),         // resourse_id
		fmt.Sprintf("$%d", argIndex+4),         // external_resource_id (ARN)
		fmt.Sprintf("$%d", argIndex+5),         // service_name
		fmt.Sprintf("$%d", argIndex+6),         // region
		fmt.Sprintf("$%d", argIndex+7),         // type
		fmt.Sprintf("$%d", argIndex+8),         // name
		fmt.Sprintf("$%d", argIndex+9),         // arn
		fmt.Sprintf("$%d::jsonb", argIndex+10), // tags
		fmt.Sprintf("$%d::jsonb", argIndex+11), // meta
		fmt.Sprintf("$%d", argIndex+12),        // created_at
		fmt.Sprintf("$%d", argIndex+13),        // updated_at
		fmt.Sprintf("$%d", argIndex+14),        // first_seen
		fmt.Sprintf("$%d", argIndex+15),        // last_seen
	}
	args = append(args,
		tenantID,
		accountID,
		"AWS", // cloud_provider - required NOT NULL field
		params.ResourceId,
		resourceArn, // external_resource_id - use ARN from fetched resource
		params.ServiceName,
		params.Region,
		resourceType,  // Use actual type from AWS API
		resourceName,  // name from fetched resource
		resourceArn,   // arn from fetched resource
		resourceTags,  // tags from fetched resource
		finalMetaJson, // merged meta (fetched + param updates)
		now,           // created_at
		now,           // updated_at
		now,           // first_seen
		now,           // last_seen
	)
	argIndex += 16

	// Add status and is_active to INSERT if provided (optional fields)
	if finalStatus != "" {
		insertColumns = append(insertColumns, "status", "is_active")
		insertPlaceholders = append(insertPlaceholders, fmt.Sprintf("$%d", argIndex), fmt.Sprintf("$%d", argIndex+1))
		// is_active is true for all statuses except "Deleted"
		args = append(args, finalStatus, finalStatus != "Deleted")
		argIndex += 2
	}

	// ON CONFLICT - use the unique constraint (account, external_resource_id)
	// external_resource_id is set to resourceArn above, which uniquely identifies AWS resources
	// This matches the database constraint added in migration V189
	conflictColumns := []string{"account", "external_resource_id"}

	// UPDATE clause - what to update when resource already exists
	updateSetClauses := []string{
		fmt.Sprintf("updated_at = $%d", argIndex),
		fmt.Sprintf("name = $%d", argIndex+1),
		fmt.Sprintf("type = $%d", argIndex+2),
		fmt.Sprintf("arn = $%d", argIndex+3),
		fmt.Sprintf("external_resource_id = $%d", argIndex+4), // Update ARN if we get it later
		fmt.Sprintf("tags = $%d::jsonb", argIndex+5),
		fmt.Sprintf("resourse_id = $%d", argIndex+6),
		fmt.Sprintf("service_name = $%d", argIndex+7),
		fmt.Sprintf("region = $%d", argIndex+8),
	}
	args = append(args, now, resourceName, resourceType, resourceArn, resourceArn, resourceTags, params.ResourceId, params.ServiceName, params.Region)
	argIndex += 9

	// Update status if provided - with timestamp check to prevent out-of-order updates
	// Extract last_state_change timestamp if available for ordering check
	var stateChangeTimestamp string
	if params.UpdateMeta && params.MetaUpdates != nil {
		if ts, ok := params.MetaUpdates["last_state_change"].(string); ok {
			stateChangeTimestamp = ts
		}
	}

	if finalStatus != "" {
		if stateChangeTimestamp != "" {
			// Only update status if this event is newer or equal to existing state
			// This prevents late-arriving events from overwriting more recent state changes
			// CASE expression: update if no existing timestamp OR new timestamp >= existing timestamp
			updateSetClauses = append(updateSetClauses, fmt.Sprintf(
				"status = CASE WHEN (cloud_resourses.meta->>'last_state_change' IS NULL OR cloud_resourses.meta->>'last_state_change' <= $%d) THEN $%d ELSE cloud_resourses.status END",
				argIndex, argIndex+1))
			args = append(args, stateChangeTimestamp, finalStatus)
			argIndex += 2
			logger.Info("eventprocessor: conditional status update with timestamp ordering",
				"newStatus", finalStatus,
				"stateChangeTimestamp", stateChangeTimestamp,
				"resourceId", params.ResourceId)
		} else {
			// No timestamp available - update unconditionally (backward compatible behavior)
			updateSetClauses = append(updateSetClauses, fmt.Sprintf("status = $%d", argIndex))
			args = append(args, finalStatus)
			argIndex++
			logger.Debug("eventprocessor: updating status without timestamp check (no last_state_change available)",
				"newStatus", finalStatus,
				"resourceId", params.ResourceId)
		}

		// Update is_active based on status (is_active = true for all statuses except "Deleted")
		updateSetClauses = append(updateSetClauses, fmt.Sprintf("is_active = $%d", argIndex))
		args = append(args, finalStatus != "Deleted")
		argIndex++
	}

	// Update last_seen if requested (default to true)
	if params.UpdateLastSeen || (!params.UpdateLastSeen && params.NewStatus == "") {
		updateSetClauses = append(updateSetClauses, fmt.Sprintf("last_seen = $%d", argIndex))
		args = append(args, now)
		argIndex++
	}

	// Update meta fields - always merge existing DB meta with finalMetaJson
	// finalMetaJson contains: fetched resource meta + param-based updates
	// This ensures we refresh AWS data while preserving existing DB fields
	// Note: If adding new parameters after this, remember to increment argIndex appropriately
	updateSetClauses = append(updateSetClauses, fmt.Sprintf("meta = COALESCE(cloud_resourses.meta, '{}'::jsonb) || $%d::jsonb", argIndex))
	args = append(args, finalMetaJson)

	// Pre-reconcile: if a row exists with the same natural key (account, resourse_id, type, region, service_name)
	// but a different external_resource_id, update its external_resource_id so the ON CONFLICT below catches it.
	// This prevents "duplicate key violates unique constraint cloud_resourses_account_resourse_service_type_region_key".
	reconcileQuery := `UPDATE cloud_resourses SET external_resource_id = $1
		WHERE account = $2 AND resourse_id = $3 AND type = $4 AND region = $5 AND service_name = $6
		AND (external_resource_id IS NULL OR external_resource_id != $1)`
	_, _ = dbms.Exec(reconcileQuery, resourceArn, accountID, params.ResourceId, resourceType, params.Region, params.ServiceName)

	// Construct UPSERT query with RETURNING xmax to detect INSERT vs UPDATE
	upsertQuery := fmt.Sprintf(
		`INSERT INTO cloud_resourses (%s) VALUES (%s)
		ON CONFLICT (%s) DO UPDATE SET %s
		RETURNING xmax`,
		strings.Join(insertColumns, ", "),
		strings.Join(insertPlaceholders, ", "),
		strings.Join(conflictColumns, ", "),
		strings.Join(updateSetClauses, ", "),
	)

	// Execute UPSERT and get xmax
	var xmax int64
	row, err := dbms.QueryRow(upsertQuery, args...)
	if err != nil {
		logger.Error("eventprocessor: failed to execute upsert query", "error", err, "query", upsertQuery, "args", args)
		return nil, fmt.Errorf("eventprocessor: failed to execute upsert query: %w", err)
	}

	err = row.Scan(&xmax)
	if err != nil {
		logger.Error("eventprocessor: failed to scan upsert result", "error", err, "query", upsertQuery, "args", args)
		return nil, fmt.Errorf("eventprocessor: failed to scan upsert result: %w", err)
	}

	// Detect if it was an INSERT or UPDATE
	// xmax = 0 means INSERT (new row)
	// xmax != 0 means UPDATE (existing row was modified)
	wasInsert := xmax == 0
	operation := "UPDATE"
	if wasInsert {
		operation = "INSERT"
	}
	// No post-INSERT UPDATE needed - all data is already in the initial INSERT
	// because we fetched resource details BEFORE the UPSERT

	logger.Info("eventprocessor: resource upserted successfully",
		"operation", operation,
		"resourceId", params.ResourceId,
		"newStatus", finalStatus,
		"xmax", xmax)

	return map[string]any{
		"operation":    operation,
		"was_insert":   wasInsert,
		"resource_id":  params.ResourceId,
		"service_name": params.ServiceName,
		"new_status":   finalStatus,
	}, nil
}

// parseLogGroupFromTaskDefMeta attempts to extract the awslogs-group from a task definition's Meta field.
// This assumes the Meta field contains the unmarshalled DescribeTaskDefinitionOutput or a relevant subset.
func parseLogGroupFromTaskDefMeta(meta map[string]any) string {
	// Navigate the structure: containerDefinitions -> logConfiguration -> options -> awslogs-group
	if containerDefsVal, ok := meta["ContainerDefinitions"]; ok {
		if containerDefs, ok := containerDefsVal.([]any); ok && len(containerDefs) > 0 {
			// Assuming the first container definition is representative or the one with awslogs config
			if containerDef, ok := containerDefs[0].(map[string]any); ok {
				if logConfig, ok := containerDef["LogConfiguration"].(map[string]any); ok {
					if options, ok := logConfig["Options"].(map[string]any); ok {
						if awslogsGroup, ok := options["awslogs-group"].(string); ok && awslogsGroup != "" {
							return awslogsGroup
						}
					}
				}
			}
		}
	}
	return "" // Log group not found in the expected structure
}

// mapToStruct converts a map to a struct using JSON tags.
func mapToStruct(m map[string]any, s any) error {
	jsonBytes, err := common.MarshalJson(m)
	if err != nil {
		return err
	}
	return common.UnmarshalJson(jsonBytes, s)
}

// parseTimeOffsets calculates absolute start and end times based on event time and string offsets.
func parseTimeOffsets(eventTime time.Time, startOffsetStr, endOffsetStr string) (time.Time, time.Time, error) {
	startDuration, err := time.ParseDuration(startOffsetStr)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("eventprocessor: invalid start_time_offset '%s': %w", startOffsetStr, err)
	}
	endDuration, err := time.ParseDuration(endOffsetStr)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("eventprocessor: invalid end_time_offset '%s': %w", endOffsetStr, err)
	}
	startTime := eventTime.Add(startDuration)
	endTime := eventTime.Add(endDuration)
	return startTime, endTime, nil
}

// extractAlarmMetricDetails extracts CloudWatch metric dimensions and statistic
// from an EventBridge CloudWatch Alarm State Change event. It navigates to
// detail.configuration.metrics[].metricStat and returns:
//   - dimensions as []map[string]string{{"Name": k, "Value": v}} for GetMetricData
//   - statistic string (e.g. "Sum", "Average", "Maximum")
func extractAlarmMetricDetails(ebEvent EventBridgeEvent) ([]map[string]string, string) {
	var detail map[string]any
	if err := common.UnmarshalJson(ebEvent.Detail, &detail); err != nil {
		return nil, ""
	}
	config, _ := detail["configuration"].(map[string]any)
	if config == nil {
		return nil, ""
	}
	metrics, _ := config["metrics"].([]any)
	if len(metrics) == 0 {
		return nil, ""
	}

	// Find the first metric that has metricStat (skip math expressions which only have "expression")
	for _, metricRaw := range metrics {
		metric, _ := metricRaw.(map[string]any)
		if metric == nil {
			continue
		}
		metricStat, _ := metric["metricStat"].(map[string]any)
		if metricStat == nil {
			continue
		}

		// Extract statistic
		stat, _ := metricStat["stat"].(string)

		// Extract dimensions
		innerMetric, _ := metricStat["metric"].(map[string]any)
		if innerMetric == nil {
			return nil, stat
		}
		dims, _ := innerMetric["dimensions"].(map[string]any)
		if len(dims) == 0 {
			return nil, stat
		}
		// Deduplicate dimensions — EventBridge sometimes sends both "LoadBalancer" and
		// "loadBalancer" with the same value. Keep only unique values by lowercased key.
		seen := make(map[string]bool, len(dims))
		var result []map[string]string
		for k, v := range dims {
			lower := strings.ToLower(k)
			if seen[lower] {
				continue
			}
			seen[lower] = true
			if vStr, ok := v.(string); ok {
				result = append(result, map[string]string{"Name": k, "Value": vStr})
			}
		}
		return result, stat
	}
	return nil, ""
}

// toLowerCamelCase converts a string to lowerCamelCase.
// Examples:
//
//	subject-name -> subjectName
//	Subject-Name -> subjectName
//	SUBJECT-NAME -> subjectName
//	SubjectName  -> subjectName
//	SUBJECTNAME  -> subjectname
//	EventID      -> eventID
//	ID           -> id
func toLowerCamelCase(s string) string {
	if s == "" {
		return ""
	}

	s = strings.ReplaceAll(s, "-", " ")
	s = strings.ReplaceAll(s, "_", " ")
	words := strings.Fields(s)

	if len(words) == 0 {
		return "" // Was all separators or empty
	}

	if len(words) == 1 {
		word := words[0]
		runes := []rune(word)
		if len(runes) == 0 {
			return ""
		}

		isAllCaps := true
		hasLower := false
		for _, r := range runes {
			if unicode.IsLower(r) {
				hasLower = true
			}
			// A character is part of "all caps" if it's upper or a digit.
			if !unicode.IsUpper(r) && !unicode.IsDigit(r) {
				isAllCaps = false
			}
		}

		if isAllCaps && !hasLower { // e.g. SUBJECTNAME, ID, AWS (all caps, no lowercase letters)
			return strings.ToLower(word)
		} else { // e.g. SubjectName, eventID, subjectName
			runes[0] = unicode.ToLower(runes[0])
			return string(runes)
		}
	}

	// Multiple words from separators
	var finalBuilder strings.Builder
	for i, word := range words {
		if len(word) == 0 {
			continue
		}
		if i == 0 {
			finalBuilder.WriteString(strings.ToLower(word)) // First word all lowercase
		} else {
			runes := []rune(word)
			finalBuilder.WriteString(strings.ToUpper(string(runes[0])))
			if len(runes) > 1 {
				finalBuilder.WriteString(strings.ToLower(string(runes[1:])))
			}
		}
	}
	return finalBuilder.String()
}

// normalizeSliceRecursive is a helper to recursively normalize items in a slice.
func normalizeSliceRecursive(inputSlice []any) []any {
	if inputSlice == nil {
		return nil
	}
	processedSlice := make([]any, len(inputSlice))
	for i, item := range inputSlice {
		switch v := item.(type) {
		case map[string]any:
			processedSlice[i] = normalizeMapKeys(v)
		case []any:
			processedSlice[i] = normalizeSliceRecursive(v)
		default:
			processedSlice[i] = item
		}
	}
	return processedSlice
}

// normalizeMapKeys recursively normalizes keys in a map to include a lowerCamelCase version.
// Original keys are preserved. If a normalized key is different, it's added.
// Values that are maps or slices of maps are also processed recursively.
//
// As per the user's comment:
// - check Map keys recursively and adds new keys with following rule
// - createNewKeys With CamelCase, (implies lowerCamelCase from examples)
// - for example subject-name, will have duplicate data as subjectName
// - similarly SubjectName, will have duplicate data as subjectName
func normalizeMapKeys(inputMap map[string]any) map[string]any {
	if inputMap == nil {
		return nil
	}

	normalized := make(map[string]any, len(inputMap)*2) // Estimate capacity

	for originalKey, value := range inputMap {
		var processedValue any
		switch v := value.(type) {
		case map[string]any:
			processedValue = normalizeMapKeys(v) // Recursive call for nested maps
		case []any:
			processedValue = normalizeSliceRecursive(v) // Recursive call for slices
		default:
			processedValue = value
		}

		// Add original key-value pair
		normalized[originalKey] = processedValue

		// Generate normalized key
		normalizedKey := toLowerCamelCase(originalKey)

		// Add normalized key-value pair if the normalized key is different from the original
		// and it's a valid, non-empty key.
		if normalizedKey != "" && normalizedKey != originalKey {
			normalized[normalizedKey] = processedValue
		}
	}
	return normalized
}

// prepareTemplateData creates the data structure to be used by Go templates.
// It uses original key names from the JSON.
func (p *TemplatedEventBridgeProcessor) prepareTemplateData(ebEvent EventBridgeEvent) any {
	var detailData map[string]any
	if err := common.UnmarshalJson(ebEvent.Detail, &detailData); err != nil {
		slog.Error("failed to unmarshal ebEvent.Detail", "error", err, "data", slog.AnyValue(ebEvent))
		detailData = make(map[string]any)
	}

	var eventMap map[string]any
	eventBytes, err := common.MarshalJson(ebEvent)
	if err != nil {
		slog.Error("unable to marshal EventBridgeEvent", "error", err, "data", slog.AnyValue(ebEvent))
		eventMap = make(map[string]any)
	} else {
		if err := common.UnmarshalJson(eventBytes, &eventMap); err != nil {
			slog.Error("unable to unmarshal EventBridgeEvent into map[string]any", "error", err, "data", string(eventBytes))
			eventMap = make(map[string]any)
		}
	}

	return struct {
		Event  map[string]any
		Detail map[string]any
	}{
		Event:  normalizeMapKeys(eventMap),
		Detail: normalizeMapKeys(detailData),
	}
}

// Process applies ALL matching rules to the EventBridgeEvent.
// It collects all matching rules, executes their actions, and returns the last matched event.
func (p *TemplatedEventBridgeProcessor) Process(ctx providers.CloudProviderContext, ebEvent EventBridgeEvent, account providers.Account) (providers.Event, error) {
	logger := ctx.GetLogger()
	templateData := p.prepareTemplateData(ebEvent)

	var matchedRuleNames []string
	var lastMatchedEvent providers.Event
	var foundMatch bool

	for _, rule := range p.ruleSet.Rules {
		if !p.matches(ebEvent, rule.Triggers, templateData) {
			continue
		}

		// Track this matching rule
		matchedRuleNames = append(matchedRuleNames, rule.Name)
		foundMatch = true

		logger.Info("eventprocessor: event matches rule", "ruleName", rule.Name, "eventId", ebEvent.ID, "account_number", account.AccountNumber)

		// Render the rule's Fingerprint once: it's the dedup key AND the
		// downstream eventId for emitting rules. Done before the ActionsOnly
		// branch so dedup applies uniformly.
		fingerprint, hasFingerprint := p.resolveFingerprint(ctx, rule, templateData)

		if d, ok := p.dedupers[rule.Name]; ok && hasFingerprint {
			if !d.Allow(fingerprint, ebEvent, account) {
				logger.Info("eventprocessor: rule execution deduped within TTL",
					"ruleName", rule.Name, "fingerprint", fingerprint,
					"ttlSeconds", rule.DedupTTLSeconds)
				continue
			}
		}

		// ActionsOnly rules (Resource_Sync_* etc.) run their actions for side
		// effects (e.g. update_cloud_resource mutating cloud_resourses) but
		// must NOT emit a downstream event. Skip event_template rendering and
		// don't update lastMatchedEvent. The event_template fields are
		// ignored. foundMatch stays true so we don't fall through to
		// DefaultEventBridgeProcessor for the same delivery.
		if rule.ActionsOnly {
			p.executeActionsOnlyRule(ctx, rule, ebEvent, account, templateData)
			continue
		}

		// EventId falls back to the source-native id or a synthesized triple
		// if the rule didn't define a Fingerprint.
		eventId := ebEvent.ID
		if eventId == "" {
			eventId = fmt.Sprintf("%s-%s-%d", ebEvent.Source, ebEvent.Account, ebEvent.Time.Unix())
		}
		if hasFingerprint {
			eventId = fingerprint
		}

		provEvent := providers.Event{
			EventId:           eventId,
			FindingId:         ebEvent.ID, // Source-native per-delivery ID for traceability
			EventName:         ebEvent.DetailType,
			Date:              ebEvent.Time,
			EventSource:       "AWS_EventBridge",
			ResourceRegion:    ebEvent.Region,
			AdditionalContext: []providers.EventEvidence{},
		}

		// Render EventName from template if provided (e.g., alarmName for CloudWatch alarms)
		if rule.EventOutput.EventName.Template != "" || rule.EventOutput.EventName.Value != "" {
			renderedVal, renderErr := p.renderField(ctx, "EventName", rule.EventOutput.EventName, templateData)
			if renderErr == nil && strings.TrimSpace(renderedVal) != "" {
				provEvent.EventName = strings.TrimSpace(renderedVal)
			} else if renderErr != nil {
				logger.Warn("eventprocessor: error rendering EventName, using default DetailType", "ruleName", rule.Name, "error", renderErr)
			}
		}

		var err error
		provEvent.Title, err = p.renderField(ctx, "Title", rule.EventOutput.Title, templateData)
		if err != nil {
			provEvent.Title = fmt.Sprintf("eventprocessor: error rendering title for %s (rule: %s)", ebEvent.ID, rule.Name)
			// Potentially collect all errors and return at the end, or fail fast
		}

		provEvent.Description, err = p.renderField(ctx, "Description", rule.EventOutput.Description, templateData)
		if err != nil {
			provEvent.Description = fmt.Sprintf("eventprocessor: error rendering description for %s (rule: %s)", ebEvent.ID, rule.Name)
			// Potentially collect all errors and return at the end, or fail fast
		}

		// Render Severity from template or value
		severityStr, err := p.renderField(ctx, "Severity", rule.EventOutput.Severity, templateData)
		if err != nil {
			provEvent.EventSeverity = providers.EventSeverityInfo // Default on error
		} else {
			provEvent.EventSeverity = providers.EventSeverityFromString(severityStr)
			if provEvent.EventSeverity == "" {
				provEvent.EventSeverity = providers.EventSeverityInfo // Default if empty or invalid
			}
		}

		// Render EventStatus from template or value
		statusStr, err := p.renderField(ctx, "EventStatus", rule.EventOutput.EventStatus, templateData)
		if err != nil {
			provEvent.EventStatus = providers.EventStatusClosed // Default on error
		} else {
			provEvent.EventStatus = providers.EventStatusFromString(statusStr)
		}

		// Default resource identification. These will be used if not overridden by templates below.
		provEvent.ResourceServiceName = getServiceNameFromEventBridgeSource(ebEvent.Source)
		provEvent.ResourceRegion = ebEvent.Region // Default to event region

		if len(ebEvent.Resources) > 0 && ebEvent.Resources[0] != "" {
			_, _, _, arnResourceType, arnResourceID := parseARN(ebEvent.Resources[0])
			provEvent.ResourceId = arnResourceID
			provEvent.ResourceType = getAwsServiceResourceType(provEvent.ResourceServiceName, arnResourceType)
		}

		// Render ResourceId from template if provided
		if rule.EventOutput.ResourceId.Template != "" || rule.EventOutput.ResourceId.Value != "" {
			renderedVal, renderErr := p.renderField(ctx, "ResourceId", rule.EventOutput.ResourceId, templateData)
			if renderErr == nil {
				provEvent.ResourceId = renderedVal
			} else {
				logger.Warn("eventprocessor: error rendering ResourceId, using default derivation if available", "ruleName", rule.Name, "eventId", ebEvent.ID, "error", renderErr)
			}
		}

		// Render ResourceType from template if provided
		if rule.EventOutput.ResourceType.Template != "" || rule.EventOutput.ResourceType.Value != "" {
			renderedVal, renderErr := p.renderField(ctx, "ResourceType", rule.EventOutput.ResourceType, templateData)
			if renderErr == nil {
				provEvent.ResourceType = renderedVal
			} else {
				logger.Warn("eventprocessor: error rendering ResourceType, using default derivation if available", "ruleName", rule.Name, "eventId", ebEvent.ID, "error", renderErr)
			}
		}

		// Render ResourceServiceName from template if provided
		if rule.EventOutput.ResourceServiceName.Template != "" || rule.EventOutput.ResourceServiceName.Value != "" {
			renderedVal, renderErr := p.renderField(ctx, "ResourceServiceName", rule.EventOutput.ResourceServiceName, templateData)
			if renderErr == nil {
				provEvent.ResourceServiceName = getServiceNameFromEventBridgeSource(renderedVal)
			} else {
				logger.Warn("eventprocessor: error rendering ResourceServiceName, using default derivation if available", "ruleName", rule.Name, "eventId", ebEvent.ID, "error", renderErr)
			}
		}

		// Render ResourceRegion from template if provided
		if rule.EventOutput.ResourceRegion.Template != "" || rule.EventOutput.ResourceRegion.Value != "" {
			renderedVal, renderErr := p.renderField(ctx, "ResourceRegion", rule.EventOutput.ResourceRegion, templateData)
			if renderErr == nil && renderedVal != "" {
				provEvent.ResourceRegion = renderedVal
			} else {
				logger.Warn("eventprocessor: error rendering ResourceRegion, using default derivation if available", "ruleName", rule.Name, "eventId", ebEvent.ID, "error", renderErr)
			}
		}

		// Store the parsed detail as Raw event data
		// Type assert templateData to access its 'Detail' field.
		if actualTemplateData, ok := templateData.(struct {
			Event  map[string]any
			Detail map[string]any
		}); ok {
			provEvent.Raw = actualTemplateData.Detail
		} else {
			logger.Error("eventprocessor: unexpected type for templateData, cannot set Raw event data", "actualType", reflect.TypeOf(templateData))
			provEvent.Raw = make(map[string]any) // Default to an empty map or nil
		}

		// --- Execute Actions ---
		actionsAccount := account
		logger.Info("eventprocessor: executing actions for rule",
			"ruleName", rule.Name,
			"actionCount", len(rule.Actions),
			"eventId", eventId)

		for _, actionDef := range rule.Actions {
			actionLogger := logger.With("actionName", actionDef.Name, "actionType", actionDef.Type, "eventId", eventId)
			actionLogger.Info("eventprocessor: starting action execution")
			actionResult, err := p.executeAction(ctx, actionsAccount, ebEvent, actionDef, templateData)
			if err != nil {
				if errors.Is(err, ErrEventActionResourceMissing) {
					actionLogger.Warn("eventprocessor: skipping action, target resource gone", "error", err)
				} else {
					actionLogger.Error("eventprocessor: failed to execute action", "error", err)
				}
				provEvent.AdditionalContext = append(provEvent.AdditionalContext, providers.EventEvidence{
					Type:    providers.EventEvidenceTypeText,
					Insight: []string{fmt.Sprintf("Error executing action: %s", actionDef.Name)},
					Data:    err.Error(),
					AdditionalInfo: map[string]string{
						"action_name": actionDef.Name,
						"action_type": actionDef.Type,
					},
				})
			} else {
				actionResultJson, jsonErr := common.MarshalJson(actionResult)
				if jsonErr != nil {
					actionLogger.Error("eventprocessor: failed to marshal action result to JSON", "error", jsonErr)
					provEvent.AdditionalContext = append(provEvent.AdditionalContext, providers.EventEvidence{
						Type:    providers.EventEvidenceTypeText,
						Insight: []string{fmt.Sprintf("Error marshalling result for action: %s", actionDef.Name)},
						Data:    fmt.Sprintf("Original result: %+v, Marshalling error: %s", actionResult, jsonErr.Error()),
					})
				} else {
					provEvent.AdditionalContext = append(provEvent.AdditionalContext, providers.EventEvidence{
						Type:    providers.EventEvidenceTypeJson,
						Insight: []string{actionDef.Name, actionDef.Description},
						Data:    string(actionResultJson),
						AdditionalInfo: map[string]string{
							"action_name": actionDef.Name,
							"action_type": actionDef.Type,
						},
					})
				}
			}
		}

		provEvent.Labels = map[string]string{
			"aws_region":         ebEvent.Region,
			"aws_service_name":   provEvent.ResourceServiceName,
			"aws_event_instance": provEvent.ResourceId,
			"aws_event_type":     ebEvent.DetailType,
			"aws_event_source":   ebEvent.Source,
			"aws_event_status":   string(provEvent.EventStatus),
			"aws_event_severity": string(provEvent.EventSeverity),
		}

		// Render label templates from rule and merge (non-empty values override base labels)
		for labelKey, labelTpl := range rule.EventOutput.Labels {
			rendered, renderErr := p.renderField(ctx, "Label:"+labelKey, labelTpl, templateData)
			if renderErr != nil {
				logger.Warn("eventprocessor: error rendering label template", "label", labelKey, "ruleName", rule.Name, "error", renderErr)
				continue
			}
			rendered = strings.TrimSpace(rendered)
			if rendered != "" {
				provEvent.Labels[labelKey] = rendered
			}
		}

		// Post-process CloudWatch alarm events with the shared enrichment function.
		// This overrides template-rendered resource identification and labels
		// with authoritative values from cloudwatchNamespaceServiceMap.
		if ebEvent.DetailType == "CloudWatch Alarm State Change" {
			if alarmInfo, ok := extractCloudWatchAlarmInfoFromEB(ebEvent); ok {
				var awsCfg *aws.Config
				cfg, cfgErr := getAwsConfigFromAccount(ctx.GetContext(), account)
				if cfgErr == nil {
					cfg.Region = ebEvent.Region
					awsCfg = &cfg
				}
				enrichment := EnrichCloudWatchAlarm(ctx.GetContext(), alarmInfo, awsCfg != nil, awsCfg, nil)
				provEvent.ResourceId = enrichment.ResourceId
				provEvent.ResourceType = enrichment.ResourceType
				provEvent.ResourceServiceName = enrichment.ResourceServiceName
				for k, v := range enrichment.Labels {
					provEvent.Labels[k] = v
				}
			}
		}

		// Store this event as the last matched event
		lastMatchedEvent = provEvent
	}

	// Log all matched rules for debugging
	if foundMatch {
		logger.Info("eventprocessor: finished processing all matching rules",
			"eventId", ebEvent.ID,
			"totalMatches", len(matchedRuleNames),
			"matchedRules", matchedRuleNames)
		// If ONLY ActionsOnly rules matched, lastMatchedEvent has its zero value
		// (EventId == ""), which the SQS consumer treats as "no event to publish"
		// — exactly the desired behavior for Resource_Sync_* rules.
		return lastMatchedEvent, nil
	}

	logger.Info("eventprocessor: no specific rule matched, using DefaultEventBridgeProcessor for event", "eventId", ebEvent.ID, "source", ebEvent.Source, "detailType", ebEvent.DetailType, "payload", slog.AnyValue(ebEvent))
	return NewDefaultEventBridgeProcessor().Process(ctx, ebEvent, account)
}

// DefaultEventBridgeProcessor is a basic implementation of EventProcessor
// that converts an EventBridgeEvent to a providers.Event.
type DefaultEventBridgeProcessor struct{}

// NewDefaultEventBridgeProcessor creates a new DefaultEventBridgeProcessor.
func NewDefaultEventBridgeProcessor() *DefaultEventBridgeProcessor {
	return &DefaultEventBridgeProcessor{}
}

// Process converts an EventBridgeEvent to a providers.Event.
func (p *DefaultEventBridgeProcessor) Process(ctx providers.CloudProviderContext, ebEvent EventBridgeEvent, account providers.Account) (providers.Event, error) {
	provEvent := providers.Event{
		EventId:        ebEvent.ID,
		EventName:      ebEvent.DetailType,
		Date:           ebEvent.Time,
		EventSource:    "AWS_EventBridge", // Indicate original source
		ResourceRegion: ebEvent.Region,
		EventStatus:    providers.EventStatusClosed, // Default for EventBridge events
		EventSeverity:  providers.EventSeverityInfo, // Default, can be adjusted based on DetailType
	}

	provEvent.ResourceServiceName = getServiceNameFromEventBridgeSource(ebEvent.Source)

	// Extract ResourceId and ResourceType from the first ARN in Resources
	if len(ebEvent.Resources) > 0 && ebEvent.Resources[0] != "" {
		arn := ebEvent.Resources[0]
		_, _, _, arnResourceType, arnResourceID := parseARN(arn)

		provEvent.ResourceId = arnResourceID
		// Map ARN resource type to a canonical service resource type if needed
		provEvent.ResourceType = getAwsServiceResourceType(provEvent.ResourceServiceName, arnResourceType)
		if provEvent.ResourceType == "" && arnResourceType != "" { // Fallback if no mapping
			provEvent.ResourceType = arnResourceType
		}
	}

	// If ResourceType or ResourceId is still empty, they might be in the Detail
	// This part would require specific handlers per event.Source and event.DetailType

	provEvent.Title = fmt.Sprintf("%s: %s", ebEvent.Source, ebEvent.DetailType)

	// Unmarshal Detail into Raw field
	var rawDetail map[string]any
	if err := common.UnmarshalJson(ebEvent.Detail, &rawDetail); err != nil {
		ctx.GetLogger().Warn("Failed to unmarshal EventBridge event detail into map[string]any", "error", err, "eventId", ebEvent.ID)
		// Store the raw JSON string if unmarshalling to map fails, or handle error as appropriate
		provEvent.Raw = map[string]any{"detail_raw": string(ebEvent.Detail)}
	} else {
		provEvent.Raw = rawDetail
	}

	// Set labels so downstream evidence actions (cloud_resource, cloud_metrics,
	// cloud_logs, etc.) can pass CanAutoExecute checks.
	provEvent.Labels = map[string]string{
		"aws_region":         ebEvent.Region,
		"aws_service_name":   provEvent.ResourceServiceName,
		"aws_event_instance": provEvent.ResourceId,
		"aws_event_type":     ebEvent.DetailType,
		"aws_event_source":   ebEvent.Source,
		"aws_event_status":   string(provEvent.EventStatus),
		"aws_event_severity": string(provEvent.EventSeverity),
	}

	return provEvent, nil
}
