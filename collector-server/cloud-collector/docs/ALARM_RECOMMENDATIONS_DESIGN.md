# CloudWatch Alarm Recommendations - Design Document

## Table of Contents
1. [Overview](#overview)
2. [Architecture](#architecture)
3. [Core Components](#core-components)
4. [Data Flow](#data-flow)
5. [Alarm Template Structure](#alarm-template-structure)
6. [Metric Math Support](#metric-math-support)
7. [Threshold Calculation](#threshold-calculation)
8. [Rule Evaluation](#rule-evaluation)
9. [Recommendation Lifecycle](#recommendation-lifecycle)
10. [Extension Guide](#extension-guide)

---

## Overview

The CloudWatch Alarm Recommendations system automatically detects missing monitoring alarms for AWS resources and generates actionable recommendations that users can apply directly from the UI to create CloudWatch alarms.

### Key Features
- **YAML-driven Configuration**: Alarm templates defined in YAML for easy maintenance
- **Dynamic Threshold Calculation**: Instance-type-aware thresholds (e.g., t3 instances: 70% CPU vs m5: 80%)
- **Metric Math Support**: Complex alarms using multiple metrics and expressions
- **Conditional Recommendations**: Rule-based evaluation for context-aware recommendations
- **One-Click Application**: Users can create alarms directly from recommendations

### Supported Services
- **EC2**: CPU utilization, status checks (instance/system/EBS)
- **RDS**: CPU, memory, storage, read/write latency
- **ElastiCache**: CPU, memory, swap, evictions, replication lag
- **ALB**: HTTP error rate (metric math), rejected connections, response time
- **Lambda**: Error rate, throttles, duration, concurrent executions
- **DynamoDB**: Read/write capacity, throttles, replication latency
- **SQS**: Message visibility timeout, message age
- **SNS**: Failed notifications, SMS spend, DLQ notifications
- **S3**: Request latency

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     Cloud Collector Service                      │
└─────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────┐
│                  Resource Collection Phase                       │
│  • GetResources() fetches EC2, RDS, ALB, etc.                   │
│  • Includes AlarmDetails in Meta for existing alarms             │
└─────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────┐
│               Recommendation Generation Phase                    │
│                                                                  │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ 1. Load Alarm Templates                                   │  │
│  │    LoadAlarmTemplates("ec2") → []AlarmTemplate           │  │
│  └──────────────────────────────────────────────────────────┘  │
│                          │                                       │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ 2. Filter Templates                                       │  │
│  │    ShouldRecommendAlarm(resource, template)              │  │
│  │    • Check metric_type (NATIVE vs CONDITIONAL)           │  │
│  │    • Evaluate conditions if CONDITIONAL                   │  │
│  └──────────────────────────────────────────────────────────┘  │
│                          │                                       │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ 3. Check Existing Alarms                                  │  │
│  │    IsAlarmMissing(resource, template, resourceId)        │  │
│  │    • Parse AlarmDetails from resource.Meta               │  │
│  │    • Match namespace + metric + dimensions               │  │
│  └──────────────────────────────────────────────────────────┘  │
│                          │                                       │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ 4. Calculate Threshold                                    │  │
│  │    CalculateThreshold(resource, template)                │  │
│  │    • Instance family (t3 → 70%, m5 → 80%)               │  │
│  │    • Memory size, storage type, etc.                     │  │
│  └──────────────────────────────────────────────────────────┘  │
│                          │                                       │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ 5. Build Alarm Configuration                              │  │
│  │    • Simple metric: MetricName + Dimensions              │  │
│  │    • Metric math: Metrics array with expressions         │  │
│  └──────────────────────────────────────────────────────────┘  │
│                          │                                       │
│  ┌──────────────────────────────────────────────────────────┐  │
│  │ 6. Create Recommendation                                  │  │
│  │    • CategoryName: Configuration                         │  │
│  │    • RuleName: aws_{service}_{metric}_alarm_missing      │  │
│  │    • Data: includes alarm_config, reason, threshold      │  │
│  └──────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────┐
│                   Storage in PostgreSQL                          │
│  • Table: recommendation                                         │
│  • Column: recommendation (JSONB)                                │
│  • Fields: rule_name, category, severity, status, data          │
└─────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────┐
│                      UI Display                                  │
│  • Configuration tab shows recommendations                       │
│  • Displays: rule name, resource, reason, severity              │
│  • User clicks "Apply" → triggers ApplyRecommendation()         │
└─────────────────────────────────────────────────────────────────┘
                                  │
                                  ▼
┌─────────────────────────────────────────────────────────────────┐
│                   Alarm Creation Phase                           │
│  • ApplyRecommendation() extracts alarm_config from Data        │
│  • CreateCloudWatchAlarmFromRecommendation()                    │
│  • Calls AWS CloudWatch PutMetricAlarm API                      │
└─────────────────────────────────────────────────────────────────┘
```

---

## Core Components

### 1. Alarm Template Loader (`aws_alarm_template_loader.go`)

**Purpose**: Load and parse YAML alarm templates for a service.

**Key Functions**:
```go
func LoadAlarmTemplates(serviceName string) ([]AlarmTemplate, error)
```

**How it works**:
1. Uses `embed.FS` to read `alarm_templates/` directory
2. Finds `{serviceName}.yaml` file (e.g., `ec2.yaml`, `alb.yaml`)
3. Parses YAML into `[]AlarmTemplate` struct
4. Returns templates for the service

**Example**:
```go
ec2Templates, err := LoadAlarmTemplates("ec2")
// Returns 4 templates: CPU, StatusCheckInstance, StatusCheckSystem, StatusCheckEBS
```

---

### 2. Alarm Checker (`aws_alarm_checker.go`)

**Purpose**: Determine if an alarm already exists for a resource.

**Key Functions**:
```go
func IsAlarmMissing(resource Resource, template AlarmTemplate, resourceId string) (bool, error)
func ShouldRecommendAlarm(resource Resource, template AlarmTemplate) bool
```

**Logic**:

#### `ShouldRecommendAlarm()`:
```go
if template.MetricType == "NATIVE" {
    return true  // Always recommend
}

if template.MetricType == "CONDITIONAL" {
    return EvaluateConditions(resource, template.Conditions)
}
```

#### `IsAlarmMissing()`:
```go
// 1. Extract AlarmDetails from resource.Meta
alarmDetails := resource.Meta["AlarmDetails"]

// 2. For each existing alarm:
for _, alarm := range alarmDetails {
    // 3. Match namespace + metric name
    if alarm.Namespace == template.Configuration.Namespace &&
       alarm.MetricName == template.Configuration.MetricName {

        // 4. Match dimensions (e.g., InstanceId = i-xxx)
        if dimensionsMatch(alarm.Dimensions, resourceId) {
            return false  // Alarm exists
        }
    }
}

return true  // Alarm is missing
```

---

### 3. Threshold Calculator (`aws_alarm_threshold_calculator.go`)

**Purpose**: Calculate dynamic thresholds based on resource properties.

**Key Functions**:
```go
func CalculateThreshold(resource Resource, template AlarmTemplate) (float64, error)
```

**Threshold Rules** (from `ThresholdRules` struct):

```yaml
threshold_rules:
  default: 80.0

  by_instance_family:
    t3: 70.0   # Burstable instances
    m5: 80.0   # General purpose
    c5: 85.0   # Compute-optimized
    r5: 85.0   # Memory-optimized

  by_instance_class:    # RDS
    db.t3: 65.0
    db.r5: 80.0

  by_storage_type:      # Latency thresholds
    gp2: 20.0           # 20ms
    io2: 8.0            # 8ms

  default_percentage: 15.0   # For memory/storage
  minimum_bytes: 1073741824  # 1GB minimum
```

**Calculation Logic**:
```go
// 1. Extract instance type (e.g., "t3.medium")
instanceType := resource.Meta["InstanceType"]
family := extractFamily(instanceType)  // "t3"

// 2. Check family-specific threshold
if threshold, ok := template.ThresholdRules.ByInstanceFamily[family]; ok {
    return threshold
}

// 3. Fall back to default
return template.ThresholdRules.Default
```

---

### 4. Rule Evaluator (`rule_evaluator.go`)

**Purpose**: Evaluate conditional rules for CONDITIONAL metric types.

**Supported Operators**:
- `exists`: Field exists in resource
- `not_exists`: Field does not exist
- `equals`: Field value equals expected value
- `not_equals`: Field value does not equal
- `contains`: String field contains substring
- `gt`, `gte`, `lt`, `lte`: Numeric comparisons
- `is_empty`, `not_empty`: String emptiness

**Example Condition** (DynamoDB replication latency):
```yaml
conditions:
  - field: "Meta.GlobalTableVersion"
    operator: "exists"
    logic: "OR"
  - field: "Meta.Replicas"
    operator: "exists"
```

**Evaluation**:
```go
func EvaluateConditions(resource Resource, conditions []Condition) bool {
    // OR logic: any condition passes → return true
    // AND logic: all conditions must pass
}
```

---

### 5. Alarm Creator (`aws_alarm_creator.go`)

**Purpose**: Create CloudWatch alarms via AWS SDK.

**Key Functions**:
```go
func CreateCloudWatchAlarm(ctx, account, config AlarmCreationConfig, region string) error
func CreateCloudWatchAlarmFromRecommendation(ctx, account, recommendation Recommendation) error
```

**Simple Metric Alarm**:
```go
input := &cloudwatch.PutMetricAlarmInput{
    AlarmName:          "aws_ec2_cpu_utilization_alarm_missing-i-xxx",
    MetricName:         "CPUUtilization",
    Namespace:          "AWS/EC2",
    Statistic:          "Average",
    Period:             300,
    EvaluationPeriods:  2,
    Threshold:          70.0,
    ComparisonOperator: "GreaterThanThreshold",
    Dimensions: []Dimension{
        {Name: "InstanceId", Value: "i-xxx"},
    },
}
```

**Metric Math Alarm** (ALB HTTP Error Rate):
```go
input := &cloudwatch.PutMetricAlarmInput{
    AlarmName:          "aws_alb_http_error_rate_alarm_missing-xxx",
    EvaluationPeriods:  5,
    Threshold:          5.0,
    ComparisonOperator: "GreaterThanThreshold",
    Metrics: []MetricDataQuery{
        {
            Id: "m1",
            MetricStat: {
                Metric: {Namespace: "AWS/ApplicationELB", MetricName: "HTTPCode_Target_4XX_Count"},
                Period: 60,
                Stat: "Sum",
            },
        },
        {
            Id: "m2",
            MetricStat: {
                Metric: {Namespace: "AWS/ApplicationELB", MetricName: "HTTPCode_Target_5XX_Count"},
                Period: 60,
                Stat: "Sum",
            },
        },
        {
            Id: "m3",
            MetricStat: {
                Metric: {Namespace: "AWS/ApplicationELB", MetricName: "RequestCount"},
                Period: 60,
                Stat: "Sum",
            },
        },
        {
            Id: "error_rate",
            Expression: "((m1 + m2) / m3) * 100",
            ReturnData: true,
        },
    },
}
```

---

## Data Flow

### Phase 1: Resource Collection

```
AWS API → GetResources() → Resource[]
```

**EC2 Example**:
```go
resource := Resource{
    Id: "i-1234567890abcdef0",
    Arn: "arn:aws:ec2:...",
    ServiceName: "AmazonEC2",
    Type: "compute-instance",
    Region: "us-east-1",
    Meta: map[string]any{
        "InstanceType": "t3.medium",
        "InstanceTypeDetails": {...},  // From Pricing API
        "AlarmDetails": []any{
            {
                "AlarmName": "cpu-alarm",
                "Namespace": "AWS/EC2",
                "MetricName": "CPUUtilization",
                "Dimensions": [...],
            },
        },
    },
}
```

---

### Phase 2: Recommendation Generation

```
GetRecommendations(resources) → Recommendation[]
```

**For each resource**:

1. **Load Templates**:
```go
templates, _ := LoadAlarmTemplates("ec2")
```

2. **Filter Templates**:
```go
for _, template := range templates {
    if !ShouldRecommendAlarm(resource, template) {
        continue  // Skip CONDITIONAL templates that don't match
    }
}
```

3. **Check Missing Alarms**:
```go
isMissing, _ := IsAlarmMissing(resource, template, resource.Id)
if !isMissing {
    continue  // Alarm already exists
}
```

4. **Calculate Threshold**:
```go
threshold, _ := CalculateThreshold(resource, template)
// t3.medium → 70% (from by_instance_family)
```

5. **Build Alarm Config**:
```go
alarmConfig := AlarmCreationConfig{
    AlarmName: "aws_ec2_cpu_utilization_alarm_missing-i-xxx",
    MetricName: "CPUUtilization",
    Namespace: "AWS/EC2",
    Threshold: 70.0,
    Dimensions: [{Name: "InstanceId", Value: "i-xxx"}],
}
```

6. **Create Recommendation**:
```go
recommendation := Recommendation{
    CategoryName: "Configuration",
    RuleName: "aws_ec2_cpu_utilization_alarm_missing",
    Severity: "Medium",
    Data: map[string]any{
        "instance_id": "i-xxx",
        "metric_name": "CPUUtilization",
        "threshold": 70.0,
        "alarm_config": alarmConfig,
        "reason": "High CPU utilization can indicate resource constraints",
    },
    ResourceId: "i-xxx",
    ResourceType: "compute-instance",
    ResourceRegion: "us-east-1",
}
```

---

### Phase 3: Storage

```sql
INSERT INTO recommendation (
    rule_name,
    category,
    severity,
    status,
    recommendation  -- JSONB column
) VALUES (
    'aws_ec2_cpu_utilization_alarm_missing',
    'Configuration',
    'Medium',
    'Open',
    '{"instance_id": "i-xxx", "reason": "...", "alarm_config": {...}}'
);
```

---

### Phase 4: Application

**User clicks "Apply" in UI**:

```go
func (ec2 *amazonEc2) ApplyRecommendation(ctx, account, recommendation) error {
    // 1. Check if this is an alarm recommendation
    if strings.HasSuffix(recommendation.RuleName, "_alarm_missing") {
        // 2. Extract alarm_config from recommendation.Data
        alarmConfig := recommendation.Data["alarm_config"]

        // 3. Create the CloudWatch alarm
        err := CreateCloudWatchAlarmFromRecommendation(ctx, account, recommendation)

        // 4. Mark recommendation as "Applied"
        return err
    }
}
```

---

## Alarm Template Structure

### File Location
```
providers/aws/alarm_templates/
├── ec2.yaml
├── rds.yaml
├── elasticache.yaml
├── alb.yaml
├── lambda.yaml
├── dynamodb.yaml
├── sqs.yaml
├── sns.yaml
└── s3.yaml
```

### Template Schema

```yaml
service_name: "AmazonEC2"  # Must match resource.ServiceName

templates:
  - name: "aws_ec2_cpu_utilization_alarm_missing"
    alarm_type: "PERFORMANCE"      # PERFORMANCE, RELIABILITY, LATENCY
    metric_type: "NATIVE"          # NATIVE (always recommend) or CONDITIONAL
    category: "Configuration"      # Maps to RecommendationCategory
    severity: "Medium"             # High, Medium, Low
    description: "Human-readable explanation for UI"

    configuration:
      # Simple metric fields
      namespace: "AWS/EC2"
      metric_name: "CPUUtilization"
      statistic: "Average"

      # OR Metric math fields (for complex alarms)
      metrics:
        - id: "m1"
          return_data: false
          metric_stat:
            metric:
              namespace: "AWS/ApplicationELB"
              metric_name: "HTTPCode_Target_4XX_Count"
            period: 60
            stat: "Sum"

        - id: "error_rate"
          return_data: true
          expression: "((m1 + m2) / m3) * 100"
          label: "HTTP Error Rate %"

      # Common fields
      period: 300
      evaluation_periods: 2
      datapoints_to_alarm: 2
      comparison_operator: "GreaterThanThreshold"
      treat_missing_data: "notBreaching"

    threshold_rules:
      default: 80.0
      by_instance_family:
        t3: 70.0
        m5: 80.0
        c5: 85.0

    # Optional: CONDITIONAL metric types only
    conditions:
      - field: "Meta.GlobalTableVersion"
        operator: "exists"
        logic: "OR"
      - field: "Meta.Replicas"
        operator: "exists"
```

---

## Metric Math Support

### Use Case
Some critical metrics require combining multiple CloudWatch metrics using math expressions.

**Example**: ALB HTTP Error Rate
```
Error Rate % = ((4XX Count + 5XX Count) / Total Requests) * 100
```

### Template Structure

```yaml
- name: "aws_alb_http_error_rate_alarm_missing"
  configuration:
    metrics:
      # Data metric 1: 4XX errors
      - id: "m1"
        return_data: false
        metric_stat:
          metric:
            namespace: "AWS/ApplicationELB"
            metric_name: "HTTPCode_Target_4XX_Count"
          period: 60
          stat: "Sum"

      # Data metric 2: 5XX errors
      - id: "m2"
        return_data: false
        metric_stat:
          metric:
            namespace: "AWS/ApplicationELB"
            metric_name: "HTTPCode_Target_5XX_Count"
          period: 60
          stat: "Sum"

      # Data metric 3: Total requests
      - id: "m3"
        return_data: false
        metric_stat:
          metric:
            namespace: "AWS/ApplicationELB"
            metric_name: "RequestCount"
          period: 60
          stat: "Sum"

      # Expression: Calculate error rate
      - id: "error_rate"
        return_data: true              # This is what gets evaluated
        expression: "((m1 + m2) / m3) * 100"
        label: "HTTP Error Rate %"
```

### Code Handling

**Detection**:
```go
if len(template.Configuration.Metrics) > 0 {
    // Metric math alarm
} else {
    // Simple metric alarm
}
```

**Build Metric Queries**:
```go
metricQueries := make([]MetricQueryConfig, len(template.Configuration.Metrics))

for i, mq := range template.Configuration.Metrics {
    query := MetricQueryConfig{
        Id: mq.Id,
        ReturnData: mq.ReturnData,
    }

    if mq.Expression != "" {
        // Math expression query
        query.Expression = mq.Expression
        query.Label = mq.Label
    } else if mq.MetricStat != nil {
        // Data metric query
        query.MetricStat = &MetricStatConfig{
            Metric: MetricInfoConfig{
                Namespace: mq.MetricStat.Metric.Namespace,
                MetricName: mq.MetricStat.Metric.MetricName,
                Dimensions: []AlarmDimension{
                    {Name: "LoadBalancer", Value: loadBalancerDimension},
                },
            },
            Period: mq.MetricStat.Period,
            Stat: mq.MetricStat.Stat,
        }
    }

    metricQueries[i] = query
}
```

---

## Threshold Calculation

### Hierarchy

1. **Instance Family** (highest priority)
2. **Instance Class** (RDS)
3. **Storage Type**
4. **Memory Size**
5. **Default** (lowest priority)

### Examples

#### EC2 CPU Utilization

```yaml
threshold_rules:
  default: 80.0
  by_instance_family:
    t3: 70.0   # Burstable - alert earlier
    c5: 85.0   # Compute-optimized - can handle higher
```

**Calculation**:
- `t3.medium` → 70%
- `m5.large` → 80% (default)
- `c5.xlarge` → 85%

#### RDS Freeable Memory

```yaml
threshold_rules:
  default_percentage: 15.0  # 15% of total memory
  minimum_bytes: 1073741824  # Never go below 1GB
```

**Calculation**:
```go
func calculateMemoryThreshold(totalMemory float64) float64 {
    threshold := totalMemory * 0.15

    if threshold < 1073741824 {  // 1GB
        threshold = 1073741824
    }

    return threshold
}
```

**Example**:
- 8GB instance: threshold = 1.2GB (15% of 8GB)
- 4GB instance: threshold = 1GB (minimum, not 600MB)

---

## Rule Evaluation

### NATIVE vs CONDITIONAL

#### NATIVE
```yaml
metric_type: "NATIVE"
# Always recommend for all resources of this type
```

**Use case**: Core monitoring metrics that every resource should have.

**Examples**:
- EC2 CPU utilization
- RDS freeable memory
- ALB target response time

---

#### CONDITIONAL
```yaml
metric_type: "CONDITIONAL"
conditions:
  - field: "Meta.GlobalTableVersion"
    operator: "exists"
```

**Use case**: Metrics only relevant for specific resource configurations.

**Examples**:
- DynamoDB replication latency (only for global tables)
- S3 request latency (only if metrics are enabled)
- SNS SMS spend (only for SMS-enabled topics)

### Condition Evaluation

**Example**: DynamoDB Replication Latency
```yaml
conditions:
  - field: "Meta.GlobalTableVersion"
    operator: "exists"
    logic: "OR"
  - field: "Meta.Replicas"
    operator: "exists"
```

**Logic**:
```go
func EvaluateConditions(resource Resource, conditions []Condition) bool {
    orConditions := []Condition{}
    andConditions := []Condition{}

    for _, cond := range conditions {
        if cond.Logic == "OR" {
            orConditions = append(orConditions, cond)
        } else {
            andConditions = append(andConditions, cond)
        }
    }

    // OR: any condition passes
    for _, cond := range orConditions {
        if evaluateCondition(resource, cond) {
            return true
        }
    }

    // AND: all conditions must pass
    for _, cond := range andConditions {
        if !evaluateCondition(resource, cond) {
            return false
        }
    }

    return len(andConditions) > 0 || len(orConditions) == 0
}
```

---

## Recommendation Lifecycle

### 1. Creation
```
Resource collected → Alarm missing → Recommendation created → Status: "Open"
```

### 2. Display
```
PostgreSQL → API → UI Configuration Tab → User sees recommendation
```

### 3. Application
```
User clicks "Apply" → ApplyRecommendation() → CreateCloudWatchAlarm()
→ Status: "Applied"
```

### 4. Verification
```
Next collection cycle → Alarm exists → No new recommendation created
```

---

## Extension Guide

### Adding a New Service

#### Step 1: Create Alarm Template

Create `providers/aws/alarm_templates/{service}.yaml`:

```yaml
service_name: "AmazonSNS"  # Match ServiceName constant

templates:
  - name: "aws_sns_failed_notifications_alarm_missing"
    alarm_type: "RELIABILITY"
    metric_type: "NATIVE"
    category: "Configuration"
    severity: "High"
    description: "Monitors failed message deliveries to detect delivery issues"

    configuration:
      namespace: "AWS/SNS"
      metric_name: "NumberOfNotificationsFailed"
      statistic: "Sum"
      period: 300
      evaluation_periods: 1
      datapoints_to_alarm: 1
      comparison_operator: "GreaterThanThreshold"
      treat_missing_data: "notBreaching"

    threshold_rules:
      default: 1.0  # Any failure is a problem
```

#### Step 2: Add Recommendation Logic

In `providers/aws/aws_{service}.go`:

```go
func (s *awsSNS) GetRecommendations(ctx, account, filter, resources) ([]Recommendation, error) {
    recommendations := []Recommendation{}

    for _, resource := range resources {
        // Load alarm templates
        templates, err := LoadAlarmTemplates("sns")
        if err != nil {
            ctx.GetLogger().Warn("Failed to load SNS alarm templates", "error", err)
            continue
        }

        for _, template := range templates {
            // Check if should recommend
            if !ShouldRecommendAlarm(resource, template) {
                continue
            }

            // Check if alarm is missing
            isMissing, _ := IsAlarmMissing(resource, template, resource.Arn)
            if !isMissing {
                continue
            }

            // Calculate threshold
            threshold, _ := CalculateThreshold(resource, template)

            // Build alarm config
            alarmConfig := AlarmCreationConfig{
                AlarmName: fmt.Sprintf("%s-%s", template.Name, resource.Id),
                MetricName: template.Configuration.MetricName,
                Namespace: template.Configuration.Namespace,
                Statistic: template.Configuration.Statistic,
                Period: template.Configuration.Period,
                EvaluationPeriods: template.Configuration.EvaluationPeriods,
                DatapointsToAlarm: template.Configuration.DatapointsToAlarm,
                Threshold: threshold,
                ComparisonOperator: template.Configuration.ComparisonOperator,
                TreatMissingData: template.Configuration.TreatMissingData,
                Dimensions: []AlarmDimension{
                    {Name: "TopicName", Value: resource.Name},
                },
            }

            // Create recommendation
            recommendation := Recommendation{
                CategoryName: RecommendationCategoryConfiguration,
                RuleName: template.Name,
                Severity: RecommendationSeverityFromString(template.Severity),
                Data: map[string]any{
                    "topic_name": resource.Name,
                    "topic_arn": resource.Arn,
                    "metric_name": template.Configuration.MetricName,
                    "threshold": threshold,
                    "alarm_config": alarmConfig,
                    "alarm_type": template.AlarmType,
                    "reason": template.Description,
                },
                Action: RecommendationActionModify,
                ResourceServiceName: resource.ServiceName,
                ResourceId: resource.Id,
                ResourceType: resource.Type,
                ResourceRegion: resource.Region,
            }

            recommendations = append(recommendations, recommendation)
        }
    }

    return recommendations, nil
}
```

#### Step 3: Add ApplyRecommendation Support

```go
func (s *awsSNS) ApplyRecommendation(ctx, account, recommendation) error {
    // Check if this is an alarm recommendation
    if strings.HasPrefix(recommendation.RuleName, "aws_sns_") &&
       strings.HasSuffix(recommendation.RuleName, "_alarm_missing") {

        err := CreateCloudWatchAlarmFromRecommendation(ctx.GetContext(), account, recommendation)
        if err != nil {
            ctx.GetLogger().Error("Failed to create CloudWatch alarm", "error", err)
            return fmt.Errorf("failed to create CloudWatch alarm: %w", err)
        }

        ctx.GetLogger().Info("Successfully created CloudWatch alarm", "ruleName", recommendation.RuleName)
        return nil
    }

    return errors.ErrUnsupported
}
```

---

### Adding a Metric Math Alarm

**Example**: Lambda Error Rate

```yaml
- name: "aws_lambda_error_rate_alarm_missing"
  configuration:
    metrics:
      # Errors
      - id: "m1"
        return_data: false
        metric_stat:
          metric:
            namespace: "AWS/Lambda"
            metric_name: "Errors"
          period: 60
          stat: "Sum"

      # Invocations
      - id: "m2"
        return_data: false
        metric_stat:
          metric:
            namespace: "AWS/Lambda"
            metric_name: "Invocations"
          period: 60
          stat: "Sum"

      # Error Rate
      - id: "error_rate"
        return_data: true
        expression: "(m1 / m2) * 100"
        label: "Error Rate %"

    period: 60
    evaluation_periods: 5
    datapoints_to_alarm: 5
    comparison_operator: "GreaterThanThreshold"
    treat_missing_data: "notBreaching"

  threshold_rules:
    default: 5.0  # 5% error rate
```

**Code**:
```go
if len(template.Configuration.Metrics) > 0 {
    // Build metric math alarm config
    metricQueries := make([]MetricQueryConfig, len(template.Configuration.Metrics))

    for i, mq := range template.Configuration.Metrics {
        query := MetricQueryConfig{Id: mq.Id, ReturnData: mq.ReturnData}

        if mq.Expression != "" {
            query.Expression = mq.Expression
            query.Label = mq.Label
        } else if mq.MetricStat != nil {
            query.MetricStat = &MetricStatConfig{
                Metric: MetricInfoConfig{
                    Namespace: mq.MetricStat.Metric.Namespace,
                    MetricName: mq.MetricStat.Metric.MetricName,
                    Dimensions: []AlarmDimension{
                        {Name: "FunctionName", Value: functionName},
                    },
                },
                Period: mq.MetricStat.Period,
                Stat: mq.MetricStat.Stat,
            }
        }

        metricQueries[i] = query
    }

    alarmConfig.Metrics = metricQueries
}
```

---

## Best Practices

### 1. Template Naming Convention
```
aws_{service}_{metric}_{alarm_type}_alarm_missing
```

Examples:
- `aws_ec2_cpu_utilization_alarm_missing`
- `aws_rds_freeable_memory_alarm_missing`
- `aws_alb_http_error_rate_alarm_missing`

### 2. Threshold Selection
- **Err on the side of fewer alerts**: Better to miss edge cases than alert fatigue
- **Use percentages for relative metrics**: CPU, memory (scales with instance size)
- **Use absolute values for counts**: Error count, failed requests (threshold: 1)

### 3. Evaluation Periods
- **Fast-changing metrics**: Shorter periods (60s), more datapoints (5 of 5)
- **Slow-changing metrics**: Longer periods (300s), fewer datapoints (2 of 2)

### 4. Severity Levels
- **High**: Impacts availability or data integrity (status checks, errors)
- **Medium**: Impacts performance (CPU, memory, response time)
- **Low**: Best practices (tagging, logging)

### 5. Description Writing
- Focus on **impact**, not just metrics
- Bad: "CPU is high"
- Good: "High CPU utilization can indicate resource constraints or inefficient application code"

---

## Testing

### Unit Tests

**Test Alarm Detection**:
```go
func TestIsAlarmMissing(t *testing.T) {
    resource := Resource{
        Meta: map[string]any{
            "AlarmDetails": []any{
                map[string]any{
                    "Namespace": "AWS/EC2",
                    "MetricName": "CPUUtilization",
                    "Dimensions": []any{
                        map[string]any{"Name": "InstanceId", "Value": "i-xxx"},
                    },
                },
            },
        },
    }

    template := AlarmTemplate{
        Configuration: AlarmConfiguration{
            Namespace: "AWS/EC2",
            MetricName: "CPUUtilization",
        },
    }

    isMissing, err := IsAlarmMissing(resource, template, "i-xxx")
    assert.False(t, isMissing)  // Alarm exists
}
```

**Test Threshold Calculation**:
```go
func TestCalculateThreshold_InstanceFamily(t *testing.T) {
    resource := Resource{
        Meta: map[string]any{
            "InstanceType": "t3.medium",
        },
    }

    template := AlarmTemplate{
        ThresholdRules: ThresholdRules{
            Default: 80.0,
            ByInstanceFamily: map[string]float64{
                "t3": 70.0,
            },
        },
    }

    threshold, err := CalculateThreshold(resource, template)
    assert.Equal(t, 70.0, threshold)
}
```

### Integration Tests

**Test End-to-End**:
```go
func TestEC2AlarmRecommendations(t *testing.T) {
    // Create mock EC2 resources
    resources := []Resource{
        {
            Id: "i-test",
            Type: "compute-instance",
            Meta: map[string]any{
                "InstanceType": "t3.medium",
                "AlarmDetails": []any{},  // No alarms
            },
        },
    }

    // Generate recommendations
    ec2Service := &amazonEc2{}
    recommendations, err := ec2Service.GetRecommendations(ctx, account, filter, resources)

    // Verify recommendations
    assert.Nil(t, err)
    assert.Greater(t, len(recommendations), 0)

    // Verify alarm config
    rec := recommendations[0]
    alarmConfig := rec.Data["alarm_config"].(AlarmCreationConfig)
    assert.Equal(t, "CPUUtilization", alarmConfig.MetricName)
    assert.Equal(t, 70.0, alarmConfig.Threshold)  // t3 threshold
}
```

---

## Troubleshooting

### Recommendations Not Generated

**Check**:
1. **Template loaded correctly**: `LoadAlarmTemplates("service")` returns templates
2. **Service name matches**: Template `service_name` == Resource `ServiceName`
3. **Conditions evaluated correctly**: CONDITIONAL templates need condition match
4. **Alarm not already exists**: Check `AlarmDetails` in resource Meta

**Debug**:
```go
ctx.GetLogger().Info("Template evaluation",
    "template", template.Name,
    "shouldRecommend", ShouldRecommendAlarm(resource, template),
    "isMissing", IsAlarmMissing(resource, template, resourceId),
)
```

### Alarm Creation Fails

**Check**:
1. **Permissions**: IAM role has `cloudwatch:PutMetricAlarm`
2. **Dimensions**: Correct dimension name/value for service
3. **Metric math**: All metric IDs are unique, return_data=true for exactly one query

**Debug**:
```go
ctx.GetLogger().Error("Alarm creation failed",
    "config", alarmConfig,
    "error", err,
)
```

### Threshold Incorrect

**Check**:
1. **Resource metadata**: `InstanceType`, `MemorySize`, etc. populated
2. **Template rules**: Correct field names in `by_instance_family`, etc.
3. **Fallback logic**: Default threshold is reasonable

---

## Future Enhancements

### 1. Auto-Tuning Thresholds
Use CloudWatch Anomaly Detection to learn baseline behavior and set dynamic thresholds.

### 2. Alarm Groups
Group related alarms (e.g., all EC2 instance alarms) for easier management.

### 3. SNS Topic Integration
Auto-create SNS topics and subscribe to alarm notifications.

### 4. Multi-Region Support
Generate alarms across all regions where resources exist.

### 5. Cost Estimation
Show estimated cost impact of creating recommended alarms.

---

## Summary

The CloudWatch Alarm Recommendations system provides:

✅ **Automated Detection**: Finds missing alarms across 9 AWS services
✅ **YAML Configuration**: Easy to add new alarms without code changes
✅ **Dynamic Thresholds**: Instance-type-aware calculations
✅ **Metric Math Support**: Complex multi-metric alarms
✅ **One-Click Application**: Users create alarms directly from UI
✅ **Scalable Architecture**: Template-driven, service-agnostic design

**Current Coverage**: 26 alarm templates across 9 services
**Lines of Code**: ~2,600 lines (templates + infrastructure)
**Backward Compatible**: Works alongside existing recommendations
