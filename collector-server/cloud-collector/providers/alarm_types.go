package providers

// AlarmTemplate defines the structure for CloudWatch/Azure Monitor/GCP alarm recommendations
type AlarmTemplate struct {
	Name           string             `yaml:"name" json:"name"`                                 // e.g., "aws_ec2_cpu_utilization_alarm_missing"
	AlarmType      string             `yaml:"alarm_type" json:"alarm_type"`                     // PERFORMANCE, RELIABILITY, LATENCY
	MetricType     string             `yaml:"metric_type" json:"metric_type"`                   // NATIVE, CONDITIONAL
	Category       string             `yaml:"category" json:"category"`                         // Configuration
	Severity       string             `yaml:"severity" json:"severity"`                         // High, Medium, Low
	Description    string             `yaml:"description" json:"description"`                   // Human-readable description
	Configuration  AlarmConfiguration `yaml:"configuration" json:"configuration"`               // CloudWatch alarm configuration
	ThresholdRules ThresholdRules     `yaml:"threshold_rules" json:"threshold_rules"`           // Dynamic threshold rules
	Conditions     []Condition        `yaml:"conditions,omitempty" json:"conditions,omitempty"` // Conditional rules for CONDITIONAL metric types
}

// AlarmConfiguration contains the CloudWatch alarm settings
type AlarmConfiguration struct {
	// Simple metric fields (for single-metric alarms)
	Namespace  string `yaml:"namespace,omitempty" json:"namespace,omitempty"`     // AWS/EC2, AWS/RDS, etc.
	MetricName string `yaml:"metric_name,omitempty" json:"metric_name,omitempty"` // CPUUtilization, FreeableMemory, etc.
	Statistic  string `yaml:"statistic,omitempty" json:"statistic,omitempty"`     // Average, Sum, Maximum, Minimum

	// Metric math fields (for multi-metric alarms with expressions)
	Metrics []MetricQuery `yaml:"metrics,omitempty" json:"metrics,omitempty"` // Metric queries for metric math

	// Common fields
	Period             int    `yaml:"period" json:"period"`                           // Seconds (300 = 5 minutes)
	EvaluationPeriods  int    `yaml:"evaluation_periods" json:"evaluation_periods"`   // Number of periods to evaluate
	DatapointsToAlarm  int    `yaml:"datapoints_to_alarm" json:"datapoints_to_alarm"` // M of N datapoints
	ComparisonOperator string `yaml:"comparison_operator" json:"comparison_operator"` // GreaterThanThreshold, LessThanThreshold, etc.
	TreatMissingData   string `yaml:"treat_missing_data" json:"treat_missing_data"`   // notBreaching, breaching, ignore, missing
}

// MetricQuery represents either a metric statistic or a metric math expression
type MetricQuery struct {
	Id         string      `yaml:"id" json:"id"`                                       // Unique ID for this metric/expression (e.g., "m1", "expr")
	MetricStat *MetricStat `yaml:"metric_stat,omitempty" json:"metric_stat,omitempty"` // Metric statistics (for data metrics)
	Expression string      `yaml:"expression,omitempty" json:"expression,omitempty"`   // Math expression (for calculated metrics)
	ReturnData bool        `yaml:"return_data" json:"return_data"`                     // Whether this metric/expression returns data
	Label      string      `yaml:"label,omitempty" json:"label,omitempty"`             // Display label for the metric
}

// MetricStat represents the statistics for a CloudWatch metric
type MetricStat struct {
	Metric MetricInfo `yaml:"metric" json:"metric"` // Metric information
	Period int        `yaml:"period" json:"period"` // Period in seconds
	Stat   string     `yaml:"stat" json:"stat"`     // Statistic (Average, Sum, Maximum, etc.)
}

// MetricInfo represents a CloudWatch metric
type MetricInfo struct {
	Namespace  string            `yaml:"namespace" json:"namespace"`                       // AWS service namespace
	MetricName string            `yaml:"metric_name" json:"metric_name"`                   // Metric name
	Dimensions map[string]string `yaml:"dimensions,omitempty" json:"dimensions,omitempty"` // Metric dimensions (will be populated at runtime)
}

// ThresholdRules defines how to calculate dynamic thresholds based on resource properties
type ThresholdRules struct {
	// Simple default threshold
	Default float64 `yaml:"default" json:"default"`

	// Instance family based (EC2: t3 -> 70%, c5 -> 85%)
	ByInstanceFamily map[string]float64 `yaml:"by_instance_family,omitempty" json:"by_instance_family,omitempty"`

	// Instance class based (RDS: db.t3 -> 65%, db.r5 -> 80%)
	ByInstanceClass map[string]float64 `yaml:"by_instance_class,omitempty" json:"by_instance_class,omitempty"`

	// Storage type based (gp2 -> 20ms, io2 -> 8ms latency)
	ByStorageType map[string]float64 `yaml:"by_storage_type,omitempty" json:"by_storage_type,omitempty"`

	// Memory size based (<4GB -> 20%, >32GB -> 10%)
	ByMemorySize map[string]float64 `yaml:"by_memory_size,omitempty" json:"by_memory_size,omitempty"`

	// Percentage-based threshold (for memory, storage)
	DefaultPercentage float64 `yaml:"default_percentage,omitempty" json:"default_percentage,omitempty"`

	// Minimum absolute value in bytes (ensures minimum threshold)
	MinimumBytes float64 `yaml:"minimum_bytes,omitempty" json:"minimum_bytes,omitempty"`
}

// AlarmCreationConfig contains the configuration for creating a CloudWatch alarm
type AlarmCreationConfig struct {
	AlarmName string `json:"alarm_name"`

	// Simple metric fields (for single-metric alarms)
	MetricName string           `json:"metric_name,omitempty"`
	Namespace  string           `json:"namespace,omitempty"`
	Statistic  string           `json:"statistic,omitempty"`
	Dimensions []AlarmDimension `json:"dimensions,omitempty"`

	// Metric math fields (for multi-metric alarms with expressions)
	Metrics []MetricQueryConfig `json:"metrics,omitempty"`

	// Common fields
	Period             int     `json:"period"`
	EvaluationPeriods  int     `json:"evaluation_periods"`
	DatapointsToAlarm  int     `json:"datapoints_to_alarm"`
	Threshold          float64 `json:"threshold"`
	ComparisonOperator string  `json:"comparison_operator"`
	TreatMissingData   string  `json:"treat_missing_data"`
}

// MetricQueryConfig represents a metric query for alarm creation (runtime version with resolved dimensions)
type MetricQueryConfig struct {
	Id         string            `json:"id"`
	MetricStat *MetricStatConfig `json:"metric_stat,omitempty"`
	Expression string            `json:"expression,omitempty"`
	ReturnData bool              `json:"return_data"`
	Label      string            `json:"label,omitempty"`
}

// MetricStatConfig represents metric statistics for alarm creation (runtime version with resolved dimensions)
type MetricStatConfig struct {
	Metric MetricInfoConfig `json:"metric"`
	Period int              `json:"period"`
	Stat   string           `json:"stat"`
}

// MetricInfoConfig represents a CloudWatch metric for alarm creation (runtime version with resolved dimensions)
type MetricInfoConfig struct {
	Namespace  string           `json:"namespace"`
	MetricName string           `json:"metric_name"`
	Dimensions []AlarmDimension `json:"dimensions"` // Resolved dimensions at runtime
}

// AlarmDimension represents a CloudWatch alarm dimension
type AlarmDimension struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Condition defines a generic rule condition for evaluating resource properties
// Used in CONDITIONAL metric types to determine if an alarm should be recommended
type Condition struct {
	Field    string      `yaml:"field" json:"field"`                     // Path to field in resource (e.g., "Meta.DBInstanceClass", "Tags.Environment")
	Operator string      `yaml:"operator" json:"operator"`               // Comparison operator (exists, equals, gt, lt, contains, etc.)
	Value    interface{} `yaml:"value,omitempty" json:"value,omitempty"` // Expected value for comparison (optional for exists/not_exists)
	Logic    string      `yaml:"logic,omitempty" json:"logic,omitempty"` // Logic combinator: "AND" or "OR" (default: "AND")
}

// Supported operators for Condition evaluation:
// - exists: Field exists in resource (non-nil)
// - not_exists: Field does not exist or is nil
// - equals: Field value equals Value (type-aware comparison)
// - not_equals: Field value does not equal Value
// - contains: String field contains Value as substring
// - gt: Field value greater than Value (numeric comparison)
// - gte: Field value greater than or equal to Value
// - lt: Field value less than Value
// - lte: Field value less than or equal to Value
// - is_empty: String field is empty or whitespace-only
// - not_empty: String field has non-whitespace content
