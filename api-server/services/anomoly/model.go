package anomoly

import (
	"encoding/json"
	"time"
)

type AnomalyTemplate struct {
	AnomalyType      AnomalyType `json:"anomaly_type" validate:"required" mapstructure:"type"`
	BufferPercenatge float64     `json:"buffer_percentage" validate:"required" mapstructure:"buffer_percentage"`
	ChangeOperator   string      `json:"change_operator" validate:"required" mapstructure:"change_operator"`
	Title            string      `json:"title" validate:"required" mapstructure:"title"`
	Description      string      `json:"description" validate:"required" mapstructure:"description"`
	AnomalyProvider  string      `json:"anomaly_provider" mapstructure:"anomaly_provider"`
}

type Anomaly struct {
	Id              string                 `json:"id" db:"id"`
	AccountId       string                 `json:"account_id" validate:"required" mapstructure:"account_id" db:"account_id"`
	Tenant          string                 `json:"tenant" validate:"required" mapstructure:"tenant" db:"tenant"`
	Name            string                 `json:"name" validate:"required" mapstructure:"name" db:"name"`
	Namespace       string                 `json:"namespace" validate:"required" mapstructure:"namespace" db:"namespace"`
	OldValue        map[string]interface{} `json:"reference_value" validate:"required" mapstructure:"reference_value"`
	CurrentValue    float64                `json:"current_value" validate:"required" mapstructure:"current_value" db:"current_value"`
	AnomalyType     AnomalyType            `json:"anomaly_type" validate:"required" mapstructure:"anomaly_type" db:"anomaly_type"`
	IsAnomaly       bool                   `json:"is_anomaly" validate:"required" mapstructure:"is_anomaly" db:"is_anomaly"`
	EvaluatedAt     *time.Time             `json:"evaluated_at,omitempty" db:"evaluated_at,omitempty"`
	PodName         string                 `json:"pod_name,omitempty" db:"pod_name,omitempty"`
	TrainingEndTime *time.Time             `json:"training_end_time,omitempty" db:"training_end_time,omitempty"`
	Insights        json.RawMessage        `json:"insights,omitempty" db:"insights,omitempty"`
}

type AnomalyType string

const (
	MetricAnomolyTypeLatency           AnomalyType = "Latency"
	MetricAnomolyTypeMemory            AnomalyType = "Memory"
	MetricAnomolyTypeCPU               AnomalyType = "CPU"
	MetricAnomolyTypeNetwork           AnomalyType = "Network"
	MetricAnomolyTypeErrorRate         AnomalyType = "ErrorRate"
	MetricAnomolyTypeReplicas          AnomalyType = "Replicas"
	MetricAnomolyTypeCloudSpendAccount AnomalyType = "CloudSpendAccount"
	MetricAnomolyTypeCloudSpendService AnomalyType = "CloudSpendService"
)

type SpendAnomalyStatus string

const (
	SpendAnomalyStatusOpen     SpendAnomalyStatus = "OPEN"
	SpendAnomalyStatusResolved SpendAnomalyStatus = "RESOLVED"
)

// OpenSpendAnomaly represents an active spend anomaly being tracked.
type OpenSpendAnomaly struct {
	ID                    int64           `db:"id"`
	Tenant                string          `db:"tenant"`
	AccountID             string          `db:"account_id"`
	Name                  string          `db:"name"`
	Namespace             *string         `db:"namespace"`
	AnomalyType           AnomalyType     `db:"anomaly_type"`
	AnomalyStatus         string          `db:"anomaly_status"`
	ReferenceValueRaw     json.RawMessage `db:"reference_value"`
	CurrentValue          float64         `db:"current_value"`
	FrozenMean            float64
	FrozenStddev          float64
	StartDate             string
	TotalImpact           float64
	MaxDailyImpact        float64
	AnomalyDays           int
	ConsecutiveNormalDays int
}

// ParseReferenceValue extracts tracking fields from reference_value JSON.
func (o *OpenSpendAnomaly) ParseReferenceValue() {
	var ref map[string]interface{}
	if err := json.Unmarshal(o.ReferenceValueRaw, &ref); err != nil {
		return
	}
	if v, ok := ref["frozen_mean"].(float64); ok {
		o.FrozenMean = v
	}
	if v, ok := ref["frozen_stddev"].(float64); ok {
		o.FrozenStddev = v
	}
	if v, ok := ref["start_date"].(string); ok {
		o.StartDate = v
	}
	if v, ok := ref["total_impact"].(float64); ok {
		o.TotalImpact = v
	}
	if v, ok := ref["max_daily_impact"].(float64); ok {
		o.MaxDailyImpact = v
	}
	if v, ok := ref["anomaly_days"].(float64); ok {
		o.AnomalyDays = int(v)
	}
	if v, ok := ref["consecutive_normal_days"].(float64); ok {
		o.ConsecutiveNormalDays = int(v)
	}
}

type SpendAnomalyResult struct {
	CloudAccount string  `db:"cloud_account"`
	AccountName  string  `db:"account_name"`
	ServiceName  string  `db:"service_name"`
	CurrentSpend float64 `db:"current_spend"`
	MeanSpend    float64 `db:"mean_spend"`
	StddevSpend  float64 `db:"stddev_spend"`
	BaselineDays int     `db:"baseline_days"`
	ZScore       float64 `db:"z_score"`
	PctChange    float64 `db:"pct_change"`
	AbsChange    float64 `db:"abs_change"`
}

type HasuraAnomalyListTemplateRequest struct {
	AccountId string `json:"account_id"`
}

type TriggerAnomalyExecuteRequest struct {
	AccountId string `json:"account_id"`
}

type TriggerAnomalyExecuteResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
}
