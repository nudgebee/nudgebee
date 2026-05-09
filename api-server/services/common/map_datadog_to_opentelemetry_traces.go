package common

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type IntOrString int

func (i *IntOrString) UnmarshalJSON(b []byte) error {
	// Try to unmarshal as number first
	var intVal int
	if err := json.Unmarshal(b, &intVal); err == nil {
		*i = IntOrString(intVal)
		return nil
	}

	// Try as string
	var strVal string
	if err := json.Unmarshal(b, &strVal); err != nil {
		return err
	}
	intVal, err := strconv.Atoi(strVal)
	if err != nil {
		return err
	}

	*i = IntOrString(intVal)
	return nil
}

// Datadog trace structures
type DatadogTrace struct {
	Data []DatadogSpan `json:"data"`
}

type DatadogSpan struct {
	Attributes DatadogAttributes `json:"attributes"`
	ID         string            `json:"id"`
	Type       string            `json:"type"`
}

type DatadogAttributes struct {
	Custom          DatadogCustom `json:"custom"`
	EndTimestamp    string        `json:"end_timestamp"`
	Env             string        `json:"env"`
	Error           interface{}   `json:"error"`
	Host            string        `json:"host"`
	IngestionReason string        `json:"ingestion_reason"`
	OperationName   string        `json:"operation_name"`
	ParentID        string        `json:"parent_id"`
	ResourceHash    string        `json:"resource_hash"`
	ResourceName    string        `json:"resource_name"`
	RetainedBy      string        `json:"retained_by"`
	Service         string        `json:"service"`
	SingleSpan      bool          `json:"single_span"`
	SpanID          string        `json:"span_id"`
	StartTimestamp  string        `json:"start_timestamp"`
	Status          string        `json:"status"`
	Tags            []string      `json:"tags"`
	TraceID         string        `json:"trace_id"`
	Type            string        `json:"type"`
}
type DatadogMongoDB struct {
	Collection string `json:"collection"`
}

type DatadogDB struct {
	System      string          `json:"system"`
	User        string          `json:"user"`
	Statement   string          `json:"statement"`
	Application string          `json:"application"`
	Instance    string          `json:"instance"`
	Operation   string          `json:"operation"`
	RowCount    int64           `json:"row_count,omitempty"`
	MongoDB     *DatadogMongoDB `json:"mongodb,omitempty"`
}

type DatadogCustom struct {
	Component        string                 `json:"component"`
	Duration         int64                  `json:"duration"`
	Env              string                 `json:"env"`
	HTTP             *DatadogHTTP           `json:"http,omitempty"`
	GRPC             *DatadogGRPC           `json:"grpc,omitempty"`
	RPC              *DatadogRPC            `json:"rpc,omitempty"`
	Language         string                 `json:"language"`
	Version          string                 `json:"version"`
	Span             DatadogSpanInfo        `json:"span"`
	ProcessID        string                 `json:"process_id"`
	RuntimeID        string                 `json:"runtime-id"`
	Service          string                 `json:"service"`
	Network          *DatadogNetwork        `json:"network,omitempty"`
	Peer             *DatadogPeer           `json:"peer,omitempty"`
	Flask            *DatadogFlask          `json:"flask,omitempty"`
	DB               *DatadogDB             `json:"db,omitempty"`
	Error            *DatadogError          `json:"error,omitempty"`
	Git              *DatadogGit            `json:"git,omitempty"`
	Issue            *DatadogIssue          `json:"issue,omitempty"`
	Thread           *DatadogThread         `json:"thread,omitempty"`
	Kafka            *DatadogKafka          `json:"kafka,omitempty"`
	Messaging        *DatadogMessaging      `json:"messaging,omitempty"`
	Net              *DatadogNet            `json:"net,omitempty"`
	Server           *DatadogServer         `json:"server,omitempty"`
	Browser          *DatadogBrowser        `json:"browser,omitempty"`
	Device           *DatadogDevice         `json:"device,omitempty"`
	OS               *DatadogOS             `json:"os,omitempty"`
	Geo              *DatadogGeo            `json:"geo,omitempty"`
	User             *DatadogUser           `json:"usr,omitempty"`
	Session          *DatadogSession        `json:"session,omitempty"`
	View             *DatadogView           `json:"view,omitempty"`
	Application      *DatadogApplication    `json:"application,omitempty"`
	Request          *DatadogRequest        `json:"request,omitempty"`
	Tags             *DatadogTags           `json:"tags,omitempty"`
	SpanLinks        []DatadogSpanLink      `json:"span_links,omitempty"`
	AdditionalFields map[string]interface{} `json:"-"` // Catch-all for custom fields not explicitly defined
}

// UnmarshalJSON custom unmarshaler for DatadogCustom to capture additional fields
func (c *DatadogCustom) UnmarshalJSON(data []byte) error {
	// Define a temporary type to avoid infinite recursion
	type Alias DatadogCustom

	// First unmarshal into the known fields
	aux := &struct {
		*Alias
	}{
		Alias: (*Alias)(c),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Then unmarshal into a map to get all fields
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Define known field names (must match json tags)
	knownFields := map[string]bool{
		"component": true, "duration": true, "env": true, "http": true,
		"grpc": true, "rpc": true, "language": true, "version": true,
		"span": true, "process_id": true, "runtime-id": true, "service": true,
		"network": true, "peer": true, "flask": true, "db": true,
		"error": true, "git": true, "issue": true, "thread": true,
		"kafka": true, "messaging": true, "net": true, "server": true,
		"browser": true, "device": true, "os": true, "geo": true,
		"usr": true, "session": true, "view": true, "application": true,
		"request": true, "tags": true, "span_links": true,
	}

	// Collect unknown fields
	c.AdditionalFields = make(map[string]interface{})
	for key, value := range raw {
		if !knownFields[key] {
			c.AdditionalFields[key] = value
		}
	}

	return nil
}

type DatadogHTTP struct {
	Host       string              `json:"host"`
	Method     string              `json:"method"`
	PathGroup  string              `json:"path_group"`
	StatusCode string              `json:"status_code"`
	URL        string              `json:"url"`
	URLDetails *DatadogURLDetails  `json:"url_details,omitempty"`
	UserAgent  string              `json:"useragent,omitempty"`
	Request    *DatadogHTTPRequest `json:"request,omitempty"`
	Route      string              `json:"route,omitempty"`
}

type DatadogURLDetails struct {
	Host   string `json:"host"`
	Path   string `json:"path"`
	Port   string `json:"port,omitempty"`
	Scheme string `json:"scheme"`
}

type DatadogHTTPRequest struct {
	Headers map[string]string `json:"headers"`
}

type DatadogGRPC struct {
	Method map[string]string `json:"method"`
}

type DatadogRPC struct {
	GRPC    *DatadogRPCGRPC `json:"grpc,omitempty"`
	Method  string          `json:"method"`
	Service string          `json:"service"`
}

type DatadogRPCGRPC struct {
	Kind       string      `json:"kind"`
	Package    string      `json:"package"`
	Path       string      `json:"path"`
	StatusCode IntOrString `json:"status_code"`
}

type DatadogSpanInfo struct {
	Kind string `json:"kind"`
}

type DatadogNetwork struct {
	Destination *DatadogDestination `json:"destination,omitempty"`
}

type DatadogDestination struct {
	IP   string      `json:"ip"`
	Port IntOrString `json:"port,omitempty"`
}

type DatadogPeerDB struct {
	Name   string `json:"name"`
	System string `json:"system"`
}

type DatadogPeer struct {
	Hostname  string         `json:"hostname"`
	Service   string         `json:"service,omitempty"`
	IPv4      string         `json:"ipv4,omitempty"`
	Port      IntOrString    `json:"port,omitempty"`
	RPC       *DatadogRPC    `json:"rpc,omitempty"`
	DB        *DatadogPeerDB `json:"db,omitempty"`
	Messaging *struct {
		Destination string `json:"destination,omitempty"`
	} `json:"messaging,omitempty"`
}

type DatadogFlask struct {
	Endpoint string `json:"endpoint,omitempty"`
	URLRule  string `json:"url_rule,omitempty"`
	Version  string `json:"version,omitempty"`
}

// Error tracking structures
type DatadogError struct {
	File        string `json:"file"`
	Fingerprint string `json:"fingerprint"`
	Handling    string `json:"handling"`
	Message     string `json:"message"`
	Stack       string `json:"stack"`
	Type        string `json:"type"`
}

func (e *DatadogError) UnmarshalJSON(data []byte) error {
	// Datadog sometimes sends error as a string (e.g. "true") or boolean instead of an object
	if len(data) == 0 || data[0] != '{' {
		// It's a non-object value — treat as an error flag with no details
		return nil
	}
	type Alias DatadogError
	var aux Alias
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	*e = DatadogError(aux)
	return nil
}

type DatadogIssue struct {
	Age              int64  `json:"age"`
	FirstSeen        int64  `json:"first_seen"`
	FirstSeenVersion string `json:"first_seen_version"`
	ID               string `json:"id"`
}

// Git metadata structures
type DatadogGitCommit struct {
	Sha string `json:"sha"`
}

type DatadogGitRepository struct {
	ID string `json:"id"`
}

type DatadogGit struct {
	Commit        *DatadogGitCommit     `json:"commit,omitempty"`
	Repository    *DatadogGitRepository `json:"repository,omitempty"`
	RepositoryURL string                `json:"repository_url,omitempty"`
}

// Thread information
type DatadogThread struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Messaging/Kafka structures
type DatadogKafka struct {
	GroupID       string `json:"group_id"`
	MessageKey    string `json:"message_key"`
	MessageOffset int64  `json:"message_offset"`
	Topic         string `json:"topic"`
}

type DatadogMessagingKafka struct {
	Partition int64 `json:"partition"`
}

type DatadogMessaging struct {
	Kafka *DatadogMessagingKafka `json:"kafka,omitempty"`
}

// Network structures
type DatadogNetOut struct {
	Bytes int64 `json:"bytes"`
}

type DatadogNet struct {
	Out *DatadogNetOut `json:"out,omitempty"`
}

type DatadogServer struct {
	Address string `json:"address"`
}

// RUM/Browser structures
type DatadogBrowser struct {
	Name         string `json:"name"`
	Version      string `json:"version"`
	VersionMajor string `json:"version_major"`
}

type DatadogDevice struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type DatadogOS struct {
	Name string `json:"name"`
}

type DatadogGeoAS struct {
	Domain string `json:"domain"`
	Name   string `json:"name"`
	Type   string `json:"type"`
}

type DatadogGeoLocation struct {
	Longitude string `json:"0"`
	Latitude  string `json:"1"`
}

type DatadogGeo struct {
	AS             *DatadogGeoAS       `json:"as,omitempty"`
	City           string              `json:"city"`
	Continent      string              `json:"continent"`
	ContinentCode  string              `json:"continent_code"`
	Country        string              `json:"country"`
	CountryIsoCode string              `json:"country_iso_code"`
	Latitude       string              `json:"latitude"`
	Longitude      string              `json:"longitude"`
	Location       *DatadogGeoLocation `json:"location,omitempty"`
}

type DatadogUser struct {
	Email string `json:"email"`
	ID    string `json:"id"`
	Name  string `json:"name"`
}

type DatadogSession struct {
	HasReplay         string `json:"has_replay"`
	IP                string `json:"ip"`
	IsReplayAvailable string `json:"is_replay_available"`
	Type              string `json:"type"`
	UserAgent         string `json:"useragent"`
}

type DatadogViewURLQuery struct {
	RedirectURI string `json:"redirect_uri,omitempty"`
}

type DatadogViewReferrerURL struct {
	URLHost      string               `json:"url_host"`
	URLPath      string               `json:"url_path"`
	URLPathGroup string               `json:"url_path_group"`
	URLQuery     *DatadogViewURLQuery `json:"url_query,omitempty"`
	URLScheme    string               `json:"url_scheme"`
}

type DatadogView struct {
	Name         string                  `json:"name"`
	Referrer     string                  `json:"referrer"`
	ReferrerURL  *DatadogViewReferrerURL `json:"referrer_url,omitempty"`
	URL          string                  `json:"url"`
	URLHost      string                  `json:"url_host"`
	URLPath      string                  `json:"url_path"`
	URLPathGroup string                  `json:"url_path_group"`
	URLScheme    string                  `json:"url_scheme"`
}

type DatadogApplication struct {
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
}

type DatadogRequest struct {
	Kind string `json:"kind"`
	Type string `json:"type"`
}

type DatadogTags struct {
	SDKVersion string `json:"sdk_version"`
	Version    string `json:"version"`
}

// Span link structure
type DatadogSpanLink struct {
	Attributes map[string]string `json:"attributes"`
	Flags      string            `json:"flags"`
	SpanID     string            `json:"span_id"`
	TraceID    string            `json:"trace_id"`
}

type OpenTelemetryTraceCount struct {
	Count int `json:"count"`
}

type OpenTelemetryTraceGroupCount struct {
	Count int `json:"count"`
}

type OpenTelemetryTraceLabelValues struct {
	Label  string   `json:"label"`
	Values []string `json:"values"`
}
type OpenTelemetryTraceHeatMap struct {
	Timestamp          string              `json:"timestamp"`
	ResourceAttributes map[string]string   `json:"resource_attributes"`
	SpanName           string              `json:"span_name"`
	StatusCode         string              `json:"status_code"`
	DurationNs         int64               `json:"duration_ns"`
	SpanAttributes     map[string]string   `json:"span_attributes"`
	TraceID            string              `json:"trace_id"`
	SpanID             string              `json:"span_id"`
	ServiceName        string              `json:"service_name"`
	EventsAttributes   []map[string]string `json:"events_attributes"`
	EventsName         []string            `json:"events_name"`
}

// OpenTelemetry trace structures
type OpenTelemetryTrace struct {
	Timestamp            string                 `json:"timestamp"`
	TraceID              string                 `json:"trace_id"`
	SpanID               string                 `json:"span_id"`
	ParentSpanID         string                 `json:"parent_span_id"`
	TraceState           string                 `json:"trace_state"`
	SpanName             string                 `json:"span_name"`
	SpanKind             string                 `json:"span_kind"`
	ServiceName          string                 `json:"service_name"`
	ResourceAttributes   map[string]string      `json:"resource_attributes"`
	SpanAttributes       map[string]string      `json:"span_attributes"`
	DurationNs           int64                  `json:"duration_ns"`
	StatusCode           string                 `json:"status_code"`
	StatusMessage        string                 `json:"status_message"`
	EventsTimestamp      []string               `json:"events_timestamp"`
	EventsName           []string               `json:"events_name"`
	EventsAttributes     []map[string]string    `json:"events_attributes"`
	LinksTraceID         []string               `json:"links_trace_id"`
	LinksSpanID          []string               `json:"links_span_id"`
	LinksTraceState      []string               `json:"links_trace_state"`
	LinksAttributes      []map[string]string    `json:"links_attributes"`
	WorkloadName         string                 `json:"workload_name"`
	WorkloadNamespace    string                 `json:"workload_namespace"`
	Resource             string                 `json:"resource"`
	DestinationName      string                 `json:"destination_name"`
	DestinationWorkload  string                 `json:"destination_workload_name"`
	DestinationNamespace string                 `json:"destination_workload_namespace"`
	Headers              string                 `json:"headers"`
	HTTPStatusCode       string                 `json:"http_status_code"`
	RequestPayload       string                 `json:"request_payload"`
	HTTPResponse         string                 `json:"http_response"`
	QueryType            string                 `json:"query_type"`
	TraceIDs             []string               `json:"trace_ids"`
	StartTime            string                 `json:"start_time"`
	EndTime              string                 `json:"end_time"`
	StartTimeUnixNano    string                 `json:"start_time_unix_nano"`
	EndTimeUnixNano      string                 `json:"end_time_unix_nano"`
	TraceSource          string                 `json:"trace_source"`
	Service              string                 `json:"service"`
	Operation            string                 `json:"operation"`
	Attributes           map[string]interface{} `json:"attributes"`
	TagFilters           map[string]interface{} `json:"tag_filters"`
	Status               map[string]interface{} `json:"status"`
}

// OTelResourceAttributes is now a type alias for map[string]string
// This allows preserving ALL OTEL resource attributes without data loss
type OTelResourceAttributes = map[string]string

// Helper functions for field mapping

// mapErrorToEvent converts Datadog error details to OpenTelemetry exception event
func mapErrorToEvent(ddError *DatadogError, issue *DatadogIssue, timestamp string) ([]string, []string, []map[string]string) {
	if ddError == nil {
		return []string{}, []string{}, []map[string]string{}
	}

	eventTimestamps := []string{timestamp}
	eventNames := []string{"exception"}
	eventAttrs := make(map[string]string)

	if ddError.Type != "" {
		eventAttrs["exception.type"] = ddError.Type
	}
	if ddError.Message != "" {
		eventAttrs["exception.message"] = ddError.Message
	}
	if ddError.Stack != "" {
		eventAttrs["exception.stacktrace"] = ddError.Stack
	}
	if ddError.File != "" {
		eventAttrs["error.file"] = ddError.File
	}
	if ddError.Fingerprint != "" {
		eventAttrs["error.fingerprint"] = ddError.Fingerprint
	}
	if ddError.Handling != "" {
		eventAttrs["error.handling"] = ddError.Handling
	}

	// Add issue metadata if available
	if issue != nil {
		if issue.ID != "" {
			eventAttrs["error.issue.id"] = issue.ID
		}
		if issue.FirstSeenVersion != "" {
			eventAttrs["error.issue.first_seen_version"] = issue.FirstSeenVersion
		}
		if issue.FirstSeen > 0 {
			eventAttrs["error.issue.first_seen"] = strconv.FormatInt(issue.FirstSeen, 10)
		}
		if issue.Age > 0 {
			eventAttrs["error.issue.age"] = strconv.FormatInt(issue.Age, 10)
		}
	}

	return eventTimestamps, eventNames, []map[string]string{eventAttrs}
}

// mapErrorAttributes adds error information to span attributes for easier querying
// This duplicates error data from events into span attributes so it can be filtered in ClickHouse queries
func mapErrorAttributes(spanAttrs map[string]string, ddError *DatadogError, issue *DatadogIssue) {
	if ddError == nil {
		return
	}

	// Add error type and message as span attributes (OTel semantic convention)
	if ddError.Type != "" {
		spanAttrs["error.type"] = ddError.Type
		spanAttrs["exception.type"] = ddError.Type // Alias for compatibility
	}
	if ddError.Message != "" {
		spanAttrs["error.message"] = ddError.Message
		spanAttrs["exception.message"] = ddError.Message // Alias for compatibility
	}
	if ddError.File != "" {
		spanAttrs["error.file"] = ddError.File
	}
	if ddError.Fingerprint != "" {
		spanAttrs["error.fingerprint"] = ddError.Fingerprint
	}
	if ddError.Handling != "" {
		spanAttrs["error.handling"] = ddError.Handling
	}

	// Add issue metadata if available
	if issue != nil {
		if issue.ID != "" {
			spanAttrs["error.issue.id"] = issue.ID
		}
		if issue.FirstSeenVersion != "" {
			spanAttrs["error.issue.first_seen_version"] = issue.FirstSeenVersion
		}
		if issue.FirstSeen > 0 {
			spanAttrs["error.issue.first_seen"] = strconv.FormatInt(issue.FirstSeen, 10)
		}
		if issue.Age > 0 {
			spanAttrs["error.issue.age"] = strconv.FormatInt(issue.Age, 10)
		}
	}
}

// mapKafkaAttributes extracts Kafka/messaging metadata to span attributes
func mapKafkaAttributes(spanAttrs map[string]string, kafka *DatadogKafka, messaging *DatadogMessaging) {
	if kafka != nil {
		spanAttrs["messaging.system"] = "kafka"
		if kafka.Topic != "" {
			spanAttrs["messaging.destination.name"] = kafka.Topic
		}
		if kafka.GroupID != "" {
			spanAttrs["messaging.kafka.consumer.group"] = kafka.GroupID
		}
		if kafka.MessageKey != "" {
			spanAttrs["messaging.kafka.message.key"] = kafka.MessageKey
		}
		if kafka.MessageOffset > 0 {
			spanAttrs["messaging.kafka.message.offset"] = strconv.FormatInt(kafka.MessageOffset, 10)
		}
	}

	if messaging != nil && messaging.Kafka != nil {
		if messaging.Kafka.Partition > 0 {
			spanAttrs["messaging.kafka.partition"] = strconv.FormatInt(messaging.Kafka.Partition, 10)
		}
	}
}

// mapDatabaseAttributes adds database-specific attributes
func mapDatabaseAttributes(spanAttrs map[string]string, db *DatadogDB, peer *DatadogPeer) {
	if db != nil {
		if db.System != "" {
			spanAttrs["db.system"] = db.System
		}
		if db.Operation != "" {
			spanAttrs["db.operation"] = db.Operation
		}
		if db.Instance != "" {
			spanAttrs["db.instance"] = db.Instance // Original Datadog name
			spanAttrs["db.name"] = db.Instance     // OTel semantic convention
		}
		if db.Statement != "" {
			spanAttrs["db.statement"] = db.Statement
		}
		if db.User != "" {
			spanAttrs["db.user"] = db.User
		}
		if db.MongoDB != nil && db.MongoDB.Collection != "" {
			spanAttrs["db.mongodb.collection"] = db.MongoDB.Collection
		}
		if db.RowCount > 0 {
			spanAttrs["db.row_count"] = strconv.FormatInt(db.RowCount, 10)
		}
	}

	// Add peer database info
	if peer != nil {
		if peer.Service != "" {
			spanAttrs["peer.service"] = peer.Service
		}
		if peer.DB != nil {
			if peer.DB.Name != "" {
				spanAttrs["peer.db.name"] = peer.DB.Name
			}
			if peer.DB.System != "" {
				spanAttrs["peer.db.system"] = peer.DB.System
			}
		}
		// Add peer hostname/IP if available
		if peer.Hostname != "" {
			spanAttrs["peer.hostname"] = peer.Hostname
		}
		if peer.IPv4 != "" {
			spanAttrs["peer.ipv4"] = peer.IPv4
		}
		if peer.Port > 0 {
			spanAttrs["peer.port"] = strconv.Itoa(int(peer.Port))
		}
		// Add peer messaging info if available
		if peer.Messaging != nil && peer.Messaging.Destination != "" {
			spanAttrs["peer.messaging.destination"] = peer.Messaging.Destination
		}
	}
}

// mapRUMAttributes extracts browser/RUM fields to span attributes
func mapRUMAttributes(spanAttrs map[string]string, custom *DatadogCustom) {
	if custom.Browser != nil {
		if custom.Browser.Name != "" {
			spanAttrs["browser.name"] = custom.Browser.Name
		}
		if custom.Browser.Version != "" {
			spanAttrs["browser.version"] = custom.Browser.Version
		}
		if custom.Browser.VersionMajor != "" {
			spanAttrs["browser.version_major"] = custom.Browser.VersionMajor
		}
	}

	if custom.Device != nil {
		if custom.Device.Name != "" {
			spanAttrs["device.name"] = custom.Device.Name
		}
		if custom.Device.Type != "" {
			spanAttrs["device.type"] = custom.Device.Type
		}
	}

	if custom.OS != nil && custom.OS.Name != "" {
		spanAttrs["os.name"] = custom.OS.Name
	}

	if custom.Geo != nil {
		if custom.Geo.City != "" {
			spanAttrs["geo.city"] = custom.Geo.City
		}
		if custom.Geo.Country != "" {
			spanAttrs["geo.country"] = custom.Geo.Country
		}
		if custom.Geo.CountryIsoCode != "" {
			spanAttrs["geo.country_iso_code"] = custom.Geo.CountryIsoCode
		}
		if custom.Geo.Continent != "" {
			spanAttrs["geo.continent"] = custom.Geo.Continent
		}
		if custom.Geo.Latitude != "" {
			spanAttrs["geo.latitude"] = custom.Geo.Latitude
		}
		if custom.Geo.Longitude != "" {
			spanAttrs["geo.longitude"] = custom.Geo.Longitude
		}
	}

	if custom.User != nil {
		if custom.User.ID != "" {
			spanAttrs["user.id"] = custom.User.ID
		}
		if custom.User.Email != "" {
			spanAttrs["user.email"] = custom.User.Email
		}
		if custom.User.Name != "" {
			spanAttrs["user.name"] = custom.User.Name
		}
	}

	if custom.Session != nil {
		if custom.Session.Type != "" {
			spanAttrs["session.type"] = custom.Session.Type
		}
		if custom.Session.IP != "" {
			spanAttrs["session.ip"] = custom.Session.IP
		}
		if custom.Session.HasReplay != "" {
			spanAttrs["session.has_replay"] = custom.Session.HasReplay
		}
	}

	if custom.View != nil {
		if custom.View.Name != "" {
			spanAttrs["view.name"] = custom.View.Name
		}
		if custom.View.URL != "" {
			spanAttrs["view.url"] = custom.View.URL
		}
		if custom.View.URLPath != "" {
			spanAttrs["view.url_path"] = custom.View.URLPath
		}
	}

	if custom.Application != nil && custom.Application.Name != "" {
		spanAttrs["application.name"] = custom.Application.Name
	}

	if custom.Request != nil {
		if custom.Request.Kind != "" {
			spanAttrs["request.kind"] = custom.Request.Kind
		}
		if custom.Request.Type != "" {
			spanAttrs["request.type"] = custom.Request.Type
		}
	}
}

// flattenCustomFields recursively flattens AdditionalFields map with dot notation
// Examples:
//
//	p44.caller = "service-a"
//	p44.user_context.tenant_id = "123"
//	pathway.hash = "abc"
//
// This makes custom attributes searchable in ClickHouse without hardcoding customer-specific schemas
func flattenCustomFields(spanAttrs map[string]string, additionalFields map[string]interface{}) {
	flattenWithPrefix(spanAttrs, "custom", additionalFields, 0, 3) // max depth 3
}

// flattenWithPrefix recursively flattens nested maps with dot notation
func flattenWithPrefix(spanAttrs map[string]string, prefix string, obj map[string]interface{}, depth int, maxDepth int) {
	if depth >= maxDepth {
		// Prevent infinite recursion, serialize remaining as JSON
		if jsonBytes, err := json.Marshal(obj); err == nil {
			spanAttrs[prefix] = string(jsonBytes)
		}
		return
	}

	for key, value := range obj {
		fullKey := prefix + "." + key

		switch v := value.(type) {
		case string:
			spanAttrs[fullKey] = v
		case float64:
			spanAttrs[fullKey] = strconv.FormatFloat(v, 'f', -1, 64)
		case int64:
			spanAttrs[fullKey] = strconv.FormatInt(v, 10)
		case int:
			spanAttrs[fullKey] = strconv.Itoa(v)
		case bool:
			spanAttrs[fullKey] = strconv.FormatBool(v)
		case map[string]interface{}:
			// Recursively flatten nested objects
			flattenWithPrefix(spanAttrs, fullKey, v, depth+1, maxDepth)
		case []interface{}:
			// For arrays, either join strings or serialize to JSON
			if isStringArray(v) {
				spanAttrs[fullKey] = joinStringArray(v)
			} else {
				if jsonBytes, err := json.Marshal(v); err == nil {
					spanAttrs[fullKey] = string(jsonBytes)
				}
			}
		case nil:
			// Skip nil values
			continue
		default:
			// Fallback: serialize to string
			spanAttrs[fullKey] = fmt.Sprintf("%v", v)
		}
	}
}

// isStringArray checks if array contains only strings
func isStringArray(arr []interface{}) bool {
	if len(arr) == 0 {
		return false
	}
	for _, item := range arr {
		if _, ok := item.(string); !ok {
			return false
		}
	}
	return true
}

// joinStringArray joins string array with commas
func joinStringArray(arr []interface{}) string {
	strs := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			strs = append(strs, s)
		}
	}
	return strings.Join(strs, ",")
}

// mapThreadAttributes adds thread information to span attributes
func mapThreadAttributes(spanAttrs map[string]string, thread *DatadogThread) {
	if thread != nil {
		if thread.ID > 0 {
			spanAttrs["thread.id"] = strconv.FormatInt(thread.ID, 10)
		}
		if thread.Name != "" {
			spanAttrs["thread.name"] = thread.Name
		}
	}
}

// mapNetworkAttributes adds network metrics to span attributes
func mapNetworkAttributes(spanAttrs map[string]string, net *DatadogNet, server *DatadogServer) {
	if net != nil && net.Out != nil && net.Out.Bytes > 0 {
		spanAttrs["net.out.bytes"] = strconv.FormatInt(net.Out.Bytes, 10)
	}

	if server != nil && server.Address != "" {
		spanAttrs["server.address"] = server.Address
	}
}

// mapSpanLinks extracts span links from Datadog to OpenTelemetry format
func mapSpanLinks(spanLinks []DatadogSpanLink) ([]string, []string, []string, []map[string]string) {
	if len(spanLinks) == 0 {
		return []string{}, []string{}, []string{}, []map[string]string{}
	}

	traceIDs := make([]string, 0, len(spanLinks))
	spanIDs := make([]string, 0, len(spanLinks))
	traceStates := make([]string, 0, len(spanLinks))
	attributes := make([]map[string]string, 0, len(spanLinks))

	for _, link := range spanLinks {
		traceIDs = append(traceIDs, link.TraceID)
		spanIDs = append(spanIDs, link.SpanID)
		traceStates = append(traceStates, link.Flags)
		attributes = append(attributes, link.Attributes)
	}

	return traceIDs, spanIDs, traceStates, attributes
}

func ResolveK8sDNS(dns string) (workload, namespace string, err error) {
	// Basic sanity check
	if dns == "" {
		return "", "", errors.New("dns string cannot be empty")
	}

	// Ensure it ends with a known cluster suffix
	if !strings.HasSuffix(dns, ".svc.cluster.local") && !strings.HasSuffix(dns, ".svc") {
		parts := strings.Split(dns, ".")
		if len(parts) == 2 {
			// Best-effort fallback: non-standard names (e.g. uppercase, underscores) are
			// valid in this path — don't validate, just return what we have.
			return parts[0], parts[1], nil
		}
	}

	parts := strings.Split(dns, ".")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid DNS format: %s", dns)
	}

	workload = parts[0]
	namespace = parts[1]

	// Validate DNS label rules
	if !IsValidK8sDNSLabel(workload) {
		return "", "", fmt.Errorf("invalid workload name: %s", workload)
	}
	if !IsValidK8sDNSLabel(namespace) {
		return "", "", fmt.Errorf("invalid namespace name: %s", namespace)
	}

	return workload, namespace, nil
}

// Mapper functions
func MapDatadogToOpenTelemetry(ddTrace DatadogTrace) []OpenTelemetryTrace {
	otelTraces := []OpenTelemetryTrace{}

	for _, span := range ddTrace.Data {
		originWorkloadNameFromTag, originWorkloadNamespaceFromTag, destWorkloadNameFromTag, destWorkloadNamespaceFromTag, destinationNameFromTag := getDestOriginWorkload(span.Attributes.Tags)

		// Priority 1: Check HTTP host for destination (most common for service-to-service calls)
		if destinationNameFromTag == "" && span.Attributes.Custom.HTTP != nil && span.Attributes.Custom.HTTP.Host != "" {
			destinationNameFromTag = span.Attributes.Custom.HTTP.Host
			// Try to resolve as K8s DNS
			workload, namespace, err := ResolveK8sDNS(destinationNameFromTag)
			if err == nil {
				destWorkloadNameFromTag = workload
				destWorkloadNamespaceFromTag = namespace
			}
		}

		// Priority 2: Check peer hostname (for non-HTTP or when http.host is not available)
		if destinationNameFromTag == "" && span.Attributes.Custom.Peer != nil && span.Attributes.Custom.Peer.Hostname != "" {
			destinationNameFromTag = span.Attributes.Custom.Peer.Hostname
			// Try to resolve as K8s DNS
			workload, namespace, err := ResolveK8sDNS(destinationNameFromTag)
			if err == nil {
				destWorkloadNameFromTag = workload
				destWorkloadNamespaceFromTag = namespace
			}
		}

		// Priority 3: Fallback to network destination IP (for IP-based connections)
		if destinationNameFromTag == "" && span.Attributes.Custom.Network != nil && span.Attributes.Custom.Network.Destination != nil && span.Attributes.Custom.Network.Destination.IP != "" {
			destinationNameFromTag = span.Attributes.Custom.Network.Destination.IP
			workload, namespace, err := ResolveK8sDNS(destinationNameFromTag)
			if err == nil {
				destWorkloadNameFromTag = workload
				destWorkloadNamespaceFromTag = namespace
			}
		}

		// Map error to event if present
		eventTimestamps, eventNames, eventAttributes := mapErrorToEvent(
			span.Attributes.Custom.Error,
			span.Attributes.Custom.Issue,
			span.Attributes.StartTimestamp,
		)

		// Map span links if present
		linksTraceID, linksSpanID, linksTraceState, linksAttributes := mapSpanLinks(span.Attributes.Custom.SpanLinks)

		otelTrace := OpenTelemetryTrace{
			Timestamp:    span.Attributes.StartTimestamp,
			TraceID:      span.Attributes.TraceID,
			SpanID:       span.Attributes.SpanID,
			ParentSpanID: span.Attributes.ParentID,
			TraceState:   "",
			SpanName:     mapOperationName(span.Attributes.OperationName, span.Attributes.Custom),
			SpanKind:     mapSpanKind(span.Attributes.Custom.Span.Kind),
			ServiceName:  span.Attributes.Service,
			ResourceAttributes: map[string]string{
				"host.id":                 extractHostID(span.Attributes.Host),
				"host.name":               span.Attributes.Host,
				"service.name":            span.Attributes.Service,
				"cloud.account.id":        "",
				"cloud.availability_zone": "",
				"cloud.region":            "",
				"container.id":            extractContainerID(span.Attributes.Tags),
			},
			SpanAttributes:       mapSpanAttributes(span.Attributes),
			WorkloadName:         originWorkloadNameFromTag,
			WorkloadNamespace:    originWorkloadNamespaceFromTag,
			Resource:             mapResource(span.Attributes.Custom),
			DestinationName:      destinationNameFromTag,
			DestinationWorkload:  destWorkloadNameFromTag,
			DestinationNamespace: destWorkloadNamespaceFromTag,
			Headers:              mapHeaders(span.Attributes),
			HTTPStatusCode:       mapHttpStatusCode(span.Attributes.Custom),
			RequestPayload:       "",
			HTTPResponse:         "",
			QueryType:            "",
			TraceIDs:             []string{},
			StartTime:            span.Attributes.StartTimestamp,
			EndTime:              span.Attributes.EndTimestamp,
			StartTimeUnixNano:    "",
			EndTimeUnixNano:      "",
			TraceSource:          "datadog",
			DurationNs:           span.Attributes.Custom.Duration,
			StatusCode:           mapStatusCode(span.Attributes.Status, span.Attributes.Custom),
			StatusMessage:        mapStatusMessage(span.Attributes.Custom),
			EventsTimestamp:      eventTimestamps,
			EventsName:           eventNames,
			EventsAttributes:     eventAttributes,
			LinksTraceID:         linksTraceID,
			LinksSpanID:          linksSpanID,
			LinksTraceState:      linksTraceState,
			LinksAttributes:      linksAttributes,
		}

		otelTraces = append(otelTraces, otelTrace)
	}

	return otelTraces
}

func MapDatadogToOpenTelemetryHeatMap(ddTrace DatadogTrace) []OpenTelemetryTraceHeatMap {
	otelTracesHeatMap := []OpenTelemetryTraceHeatMap{}

	for _, span := range ddTrace.Data {
		// Map error to event if present
		_, eventNames, eventAttributes := mapErrorToEvent(
			span.Attributes.Custom.Error,
			span.Attributes.Custom.Issue,
			span.Attributes.StartTimestamp,
		)

		otelTrace := OpenTelemetryTraceHeatMap{
			Timestamp:   span.Attributes.StartTimestamp,
			TraceID:     span.Attributes.TraceID,
			SpanID:      span.Attributes.SpanID,
			SpanName:    mapOperationName(span.Attributes.OperationName, span.Attributes.Custom),
			ServiceName: span.Attributes.Service,
			ResourceAttributes: map[string]string{
				"host.id":                 extractHostID(span.Attributes.Host),
				"host.name":               span.Attributes.Host,
				"service.name":            span.Attributes.Service,
				"cloud.account.id":        "",
				"cloud.availability_zone": "",
				"cloud.region":            "",
				"container.id":            extractContainerID(span.Attributes.Tags),
			},
			SpanAttributes:   mapSpanAttributes(span.Attributes),
			DurationNs:       span.Attributes.Custom.Duration,
			StatusCode:       mapStatusCode(span.Attributes.Status, span.Attributes.Custom),
			EventsName:       eventNames,
			EventsAttributes: eventAttributes,
		}

		otelTracesHeatMap = append(otelTracesHeatMap, otelTrace)
	}

	return otelTracesHeatMap
}

func mapOperationName(operationName string, custom DatadogCustom) string {
	// Use HTTP method if available, otherwise use operation name
	if custom.HTTP != nil && custom.HTTP.Method != "" {
		return custom.HTTP.Method
	}
	if custom.RPC != nil && custom.RPC.Method != "" {
		return custom.RPC.Method
	}
	return operationName
}

func mapResource(custom DatadogCustom) string {
	if custom.HTTP != nil {
		return custom.HTTP.URL
	}
	if custom.DB != nil {
		return custom.DB.Statement
	}
	return ""
}

func mapSpanKind(kind string) string {
	switch kind {
	case "client":
		return "SPAN_KIND_CLIENT"
	case "server":
		return "SPAN_KIND_SERVER"
	case "producer":
		return "SPAN_KIND_PRODUCER"
	case "consumer":
		return "SPAN_KIND_CONSUMER"
	case "internal":
		return "SPAN_KIND_INTERNAL"
	default:
		return "SPAN_KIND_UNSPECIFIED"
	}
}
func mapHttpStatusCode(custom DatadogCustom) string {
	if custom.HTTP != nil && custom.HTTP.StatusCode != "" {
		return custom.HTTP.StatusCode
	}
	return ""
}

func mapStatusCode(status string, custom DatadogCustom) string {
	// Check for custom error object first (most specific)
	if custom.Error != nil {
		return "STATUS_CODE_ERROR"
	}

	// Check HTTP status code for errors
	if custom.HTTP != nil && custom.HTTP.StatusCode != "" {
		if statusCode, err := strconv.Atoi(custom.HTTP.StatusCode); err == nil {
			if statusCode >= 400 {
				return "STATUS_CODE_ERROR"
			} else {
				return "STATUS_CODE_OK"
			}
		}
	}

	// Check the general span status
	if status == "error" {
		return "STATUS_CODE_ERROR"
	}

	return "STATUS_CODE_UNSET"
}

// mapStatusMessage extracts status message from error
func mapStatusMessage(custom DatadogCustom) string {
	if custom.Error != nil && custom.Error.Message != "" {
		return custom.Error.Message
	}
	return ""
}
func mapHeaders(attrs DatadogAttributes) string {
	if attrs.Custom.HTTP != nil && attrs.Custom.HTTP.Request != nil {
		return fmt.Sprintf("%v", attrs.Custom.HTTP.Request.Headers)
	}
	return ""
}

func mapSpanAttributes(attrs DatadogAttributes) map[string]string {
	spanAttrs := make(map[string]string)
	if attrs.Custom.DB != nil {
		spanAttrs["db.system"] = attrs.Custom.DB.System
		spanAttrs["db.user"] = attrs.Custom.DB.User
		spanAttrs["db.statement"] = attrs.Custom.DB.Statement
		spanAttrs["db.instance"] = attrs.Custom.DB.Instance
		spanAttrs["db.application"] = attrs.Custom.DB.Application
		spanAttrs["db.name"] = attrs.Custom.DB.Instance
		spanAttrs["db.row_count"] = strconv.FormatInt(attrs.Custom.DB.RowCount, 10)
	}

	// Add HTTP attributes if available
	if attrs.Custom.HTTP != nil {
		if attrs.Custom.HTTP.URL != "" {
			spanAttrs["http.url"] = attrs.Custom.HTTP.URL
		}
		if attrs.Custom.HTTP.Method != "" {
			spanAttrs["http.method"] = attrs.Custom.HTTP.Method
		}
		if attrs.Custom.HTTP.StatusCode != "" {
			spanAttrs["http.status_code"] = attrs.Custom.HTTP.StatusCode
		}
		if attrs.Custom.HTTP.Route != "" {
			spanAttrs["http.route"] = attrs.Custom.HTTP.Route
		}
		if attrs.Custom.HTTP.Host != "" {
			spanAttrs["http.host"] = attrs.Custom.HTTP.Host
		}
	}

	// Add network attributes if available
	if attrs.Custom.Network != nil && attrs.Custom.Network.Destination != nil {
		if attrs.Custom.Network.Destination.IP != "" {
			spanAttrs["net.peer.name"] = attrs.Custom.Network.Destination.IP
		}
		if attrs.Custom.Network.Destination.Port != 0 {
			spanAttrs["net.peer.port"] = strconv.Itoa(int(attrs.Custom.Network.Destination.Port))
		}
	}

	// Add peer hostname if available
	if attrs.Custom.Peer != nil && attrs.Custom.Peer.Hostname != "" {
		if spanAttrs["net.peer.name"] == "" {
			spanAttrs["net.peer.name"] = attrs.Custom.Peer.Hostname
		}
	}

	// Add RPC attributes if available
	if attrs.Custom.RPC != nil {
		if attrs.Custom.RPC.Service != "" {
			spanAttrs["rpc.service"] = attrs.Custom.RPC.Service
		}
		if attrs.Custom.RPC.Method != "" {
			spanAttrs["rpc.method"] = attrs.Custom.RPC.Method
		}
		if attrs.Custom.RPC.GRPC != nil {
			spanAttrs["rpc.system"] = "grpc"
			if attrs.Custom.RPC.GRPC.StatusCode != 0 {
				spanAttrs["rpc.grpc.status_code"] = strconv.Itoa(int(attrs.Custom.RPC.GRPC.StatusCode))
			}
		}
	}

	// Add component information
	if attrs.Custom.Component != "" {
		spanAttrs["component"] = attrs.Custom.Component
	}

	// Add language information (preserve both original and OTel names)
	if attrs.Custom.Language != "" {
		spanAttrs["language"] = attrs.Custom.Language               // Original Datadog name
		spanAttrs["telemetry.sdk.language"] = attrs.Custom.Language // OTel semantic convention
	}

	// Add version information (preserve both original and OTel names)
	if attrs.Custom.Version != "" {
		spanAttrs["version"] = attrs.Custom.Version         // Original Datadog name
		spanAttrs["service.version"] = attrs.Custom.Version // OTel semantic convention
	}

	// Add environment information (preserve both original and OTel names)
	if attrs.Env != "" {
		spanAttrs["env"] = attrs.Env                    // Original Datadog name
		spanAttrs["deployment.environment"] = attrs.Env // OTel semantic convention
	}
	if attrs.Custom.Env != "" {
		spanAttrs["env"] = attrs.Custom.Env                    // Original Datadog name
		spanAttrs["deployment.environment"] = attrs.Custom.Env // OTel semantic convention
	}

	// Add Datadog metadata
	if attrs.IngestionReason != "" {
		spanAttrs["dd.ingestion_reason"] = attrs.IngestionReason
	}
	if attrs.RetainedBy != "" {
		spanAttrs["dd.retained_by"] = attrs.RetainedBy
	}
	if attrs.SingleSpan {
		spanAttrs["dd.single_span"] = "true"
	}

	// Add database attributes using helper
	mapDatabaseAttributes(spanAttrs, attrs.Custom.DB, attrs.Custom.Peer)

	// Add Kafka/messaging attributes using helper
	mapKafkaAttributes(spanAttrs, attrs.Custom.Kafka, attrs.Custom.Messaging)

	// Add thread attributes using helper
	mapThreadAttributes(spanAttrs, attrs.Custom.Thread)

	// Add network metrics using helper
	mapNetworkAttributes(spanAttrs, attrs.Custom.Net, attrs.Custom.Server)

	// Add RUM/browser attributes using helper
	mapRUMAttributes(spanAttrs, &attrs.Custom)

	// Add git metadata (preserve both original and OTel names)
	if attrs.Custom.Git != nil {
		if attrs.Custom.Git.Commit != nil && attrs.Custom.Git.Commit.Sha != "" {
			spanAttrs["git.commit.sha"] = attrs.Custom.Git.Commit.Sha // Original Datadog name
			spanAttrs["git.commit.id"] = attrs.Custom.Git.Commit.Sha  // OTel semantic convention
		}
		if attrs.Custom.Git.RepositoryURL != "" {
			spanAttrs["git.repository_url"] = attrs.Custom.Git.RepositoryURL // Original Datadog name
			spanAttrs["git.repository.url"] = attrs.Custom.Git.RepositoryURL // OTel semantic convention
		}
		if attrs.Custom.Git.Repository != nil && attrs.Custom.Git.Repository.ID != "" {
			spanAttrs["git.repository.id"] = attrs.Custom.Git.Repository.ID
		}
	}

	// Extract Kubernetes information from tags
	addKubernetesAttributes(spanAttrs, attrs.Tags)

	// Add error attributes to span attributes for easier querying
	mapErrorAttributes(spanAttrs, attrs.Custom.Error, attrs.Custom.Issue)

	// Flatten custom/additional fields
	flattenCustomFields(spanAttrs, attrs.Custom.AdditionalFields)

	return spanAttrs
}

func addKubernetesAttributes(spanAttrs map[string]string, tags []string) {
	for _, tag := range tags {
		parts := strings.SplitN(tag, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key, value := parts[0], parts[1]

		// Preserve both original Datadog tag names and OTel semantic conventions
		switch key {
		case "kube_namespace":
			spanAttrs["kube_namespace"] = value     // Original Datadog name
			spanAttrs["k8s.namespace.name"] = value // OTel semantic convention
		case "kube_deployment":
			spanAttrs["kube_deployment"] = value     // Original Datadog name
			spanAttrs["k8s.deployment.name"] = value // OTel semantic convention
		case "kube_replica_set":
			spanAttrs["kube_replica_set"] = value    // Original Datadog name
			spanAttrs["k8s.replicaset.name"] = value // OTel semantic convention
		case "pod_name":
			spanAttrs["pod_name"] = value     // Original Datadog name
			spanAttrs["k8s.pod.name"] = value // OTel semantic convention
		case "container_name":
			spanAttrs["container_name"] = value     // Original Datadog name
			spanAttrs["k8s.container.name"] = value // OTel semantic convention
		case "kube_container_name":
			spanAttrs["kube_container_name"] = value // Original Datadog name
			spanAttrs["k8s.container.name"] = value  // OTel semantic convention
		case "kube_node":
			spanAttrs["kube_node"] = value     // Original Datadog name
			spanAttrs["k8s.node.name"] = value // OTel semantic convention
		case "image_name":
			spanAttrs["image_name"] = value           // Original Datadog name
			spanAttrs["container.image.name"] = value // OTel semantic convention
		case "image_tag":
			spanAttrs["image_tag"] = value           // Original Datadog name
			spanAttrs["container.image.tag"] = value // OTel semantic convention
		}
	}
}
func getDestOriginWorkload(tags []string) (string, string, string, string, string) {
	destWorkLoad := ""
	destNamespace := ""
	originWorkLoad := ""
	originNamespace := ""
	destinationName := ""
	tagsMap := make(map[string]string)
	for _, tag := range tags {
		parts := strings.SplitN(tag, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key, value := parts[0], parts[1]
		tagsMap[key] = value
	}
	if tagsMap["kube_namespace"] != "" {
		originNamespace = tagsMap["kube_namespace"]

		if tagsMap["kube_deployment"] != "" {
			originWorkLoad = tagsMap["kube_deployment"]
		} else if tagsMap["kube_replica_set"] != "" {
			originWorkLoad = tagsMap["kube_replica_set"]
		} else if tagsMap["pod_name"] != "" {
			originWorkLoad = tagsMap["pod_name"]
		}
	}
	return originWorkLoad, originNamespace, destWorkLoad, destNamespace, destinationName
}

func extractHostID(host string) string {
	// Generate a simple host ID based on the hostname
	// In a real implementation, you might want to use a more sophisticated method
	return fmt.Sprintf("%x", []byte(host))
}

func extractContainerID(tags []string) string {
	for _, tag := range tags {
		if strings.HasPrefix(tag, "container_id:") {
			return strings.TrimPrefix(tag, "container_id:")
		}
	}
	return ""
}

// Example usage
// func main() {
// 	// Example Datadog trace JSON (you would read this from your input)
// 	datadogJSON := `{
// 		"data": [
// 			{
// 				"attributes": {
// 					"custom": {
// 						"component": "http2",
// 						"duration": 4561768,
// 						"http": {
// 							"host": "otel-deployment-collector-collector.otel.svc.cluster.local",
// 							"method": "POST",
// 							"path_group": "/?/Export",
// 							"status_code": "200",
// 							"url": "http://otel-deployment-collector-collector.otel.svc.cluster.local:4317/opentelemetry.proto.collector.metrics.v1.MetricsService/Export"
// 						},
// 						"language": "javascript",
// 						"service": "frontend",
// 						"span": {
// 							"kind": "client"
// 						},
// 						"version": "2.0.2"
// 					},
// 					"end_timestamp": "2025-08-01T09:47:50.765Z",
// 					"env": "none",
// 					"error": null,
// 					"host": "k3s-example-node-pool-r3cdc",
// 					"operation_name": "http.request",
// 					"parent_id": "8322405343343255095",
// 					"resource_name": "POST",
// 					"service": "frontend",
// 					"span_id": "9051858697858281593",
// 					"start_timestamp": "2025-08-01T09:47:50.761Z",
// 					"status": "ok",
// 					"tags": [
// 						"service:frontend",
// 						"kube_namespace:od",
// 						"container_id:135af4d62e9f21f4e1d87f7313f2912974222098f29cd882355231053b18c30e"
// 					],
// 					"trace_id": "688c8d4600000000737f1f90d6c99e37",
// 					"type": "http"
// 				},
// 				"id": "AZhlB-jtAAAeVoCwDKdZ1H9z",
// 				"type": "spans"
// 			}
// 		]
// 	}`

// 	var ddTrace DatadogTrace
// 	if err := json.Unmarshal([]byte(datadogJSON), &ddTrace); err != nil {
// 		log.Fatalf("Error unmarshaling Datadog trace: %v", err)
// 	}

// 	otelTraces := MapDatadogToOpenTelemetry(ddTrace)

// 	// Convert to JSON for output
// 	otelJSON, err := json.MarshalIndent(otelTraces, "", "  ")
// 	if err != nil {
// 		log.Fatalf("Error marshaling OpenTelemetry traces: %v", err)
// 	}

// 	fmt.Println(string(otelJSON))
// }
