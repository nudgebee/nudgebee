package autopilot

import (
	"fmt"
	"time"
)

type ISOTime time.Time

func (t ISOTime) MarshalJSON() ([]byte, error) {
	stamp := fmt.Sprintf("\"%s\"", time.Time(t).UTC().Format("2006-01-02T15:04:05.999Z07:00"))
	return []byte(stamp), nil
}

type AutoOptimizeRequestResourceFilter struct {
	Id        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Type      string `json:"type,omitempty"`
	Namespace string `json:"namespace,omitempty"`
}

type AutoOptimizeVerticalRightSizeTriggerConfig struct {
	ChangePct int `json:"change_pct"`
}

type AutoOptimizeVerticalRightSizeCpuConfig struct {
	Algo      string                                     `json:"algo"`
	BufferPct int                                        `json:"buffer_pct"`
	Trigger   AutoOptimizeVerticalRightSizeTriggerConfig `json:"trigger"`
}

type AutoOptimizeVerticalRightSizeMemoryConfig struct {
	Algo      string                                     `json:"algo"`
	BufferPct int                                        `json:"buffer_pct"`
	Unit      string                                     `json:"unit"`
	Trigger   AutoOptimizeVerticalRightSizeTriggerConfig `json:"trigger"`
}

type AutoOptimizeVerticalRightSizeConfig struct {
	Cpu    *AutoOptimizeVerticalRightSizeCpuConfig    `json:"cpu,omitempty"`
	Memory *AutoOptimizeVerticalRightSizeMemoryConfig `json:"memory,omitempty"`
}

type AutoOptimizeHorizontalRightSizeConfig struct {
}

type AutoOptimizeRequestConfig struct {
	VerticalRightSize   *AutoOptimizeVerticalRightSizeConfig   `json:"vertical_rightsize,omitempty"`
	HorizontalRightSize *AutoOptimizeHorizontalRightSizeConfig `json:"horizontal_rightsize,omitempty"`
}

type AutoOptimizeRequestSchedule struct {
	Frequency *string  `json:"frequency"`
	StartDate *ISOTime `json:"start_date"`
	EndDate   *ISOTime `json:"end_date,omitempty"`
}

type AutoOptimizeNotificationConfig struct {
	Enabled bool `json:"enabled"`
}

type AutoOptimizeRequestNotification struct {
	Slack AutoOptimizeNotificationConfig `json:"slack"`
	Email AutoOptimizeNotificationConfig `json:"email"`
}

type AutoOptimizeRequest struct {
	AccountId       string                            `json:"account_id"`
	AutopilotConfig AutoOptimizeRequestConfig         `json:"autopilot_config"`
	Category        string                            `json:"category"`
	DryRun          bool                              `json:"dryrun"`
	Notification    AutoOptimizeRequestNotification   `json:"notification"`
	Schedule        AutoOptimizeRequestSchedule       `json:"schedule"`
	ResourceFilter  AutoOptimizeRequestResourceFilter `json:"resource_filter"`
	Id              *string                           `json:"id,omitempty"`
}

type AutoOptimizeResponse struct {
	Id string `json:"id"`
}
