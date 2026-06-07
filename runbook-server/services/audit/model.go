package audit

import "time"

type EventCategory string

const (
	EventCategoryUser           EventCategory = "USERS"
	EventCategoryGroup          EventCategory = "GROUPS"
	EventCategoryRole           EventCategory = "ROLES"
	EventCategoryTenant         EventCategory = "TENANTS"
	EventCategoryAccount        EventCategory = "ACCOUNTS"
	EventCategoryRecommendation EventCategory = "RECOMMENDATIONS"
	EventCategoryAutopilot      EventCategory = "AUTO_PILOT"
	// Deprecated: use EventCategoryAutomation. Retained for historical rows
	// already written under "AUTO_RUNBOOK"; new emissions must use AUTOMATION.
	EventCategoryAutorunbook   EventCategory = "AUTO_RUNBOOK"
	EventCategoryAutomation    EventCategory = "AUTOMATION"
	EventCategoryTickets       EventCategory = "TICKETS"
	EventCategoryNotifications EventCategory = "NOTIFICATIONS"
	EventCategoryK8sAgent      EventCategory = "K8S_AGENT"
	EventCategoryK8sRelay      EventCategory = "K8S_RELAY"
	EventAlertManagerRelay     EventCategory = "ALERT_MANAGER"
)

type EventType string

const (
	EventTypeUserCreate EventType = "USER_CREATE"
	EventTypeUserUpdate EventType = "USER_UPDATE"
	EventTypeUserDelete EventType = "USER_DELETE"

	EventTypeGroupCreate EventType = "GROUP_CREATE"
	EventTypeGroupUpdate EventType = "GROUP_UPDATE"
	EventTypeGroupDelete EventType = "GROUP_DELETE"

	EventTypeGroupUserCreate EventType = "GROUP_USER_CREATE"
	EventTypeGroupUserUpdate EventType = "GROUP_USER_UPDATE"
	EventTypeGroupUserDelete EventType = "GROUP_USER_DELETE"

	EventTypeRoleUserCreate    EventType = "ROLE_USER_CREATE"
	EventTypeRoleUserUpdate    EventType = "ROLE_USER_UPDATE"
	EventTypeRoleUserDelete    EventType = "ROLE_USER_DELETE"
	EventTypeRoleGroupCreate   EventType = "ROLE_GROUP_CREATE"
	EventTypeRoleGroupUpdate   EventType = "ROLE_GROUP_UPDATE"
	EventTypeRoleGroupDelete   EventType = "ROLE_GROUP_DELETE"
	EventTypeRoleAccountCreate EventType = "ROLE_ACCOUNT_CREATE"
	EventTypeRoleAccountUpdate EventType = "ROLE_ACCOUNT_UPDATE"
	EventTypeRoleAccountDelete EventType = "ROLE_ACCOUNT_DELETE"

	EventTypeUserLogin       EventType = "USER_AUTH_LOGIN"
	EventTypeUserLoginCreate EventType = "USER_AUTH_CREATE"
	EventTypeUserLoginDelete EventType = "USER_AUTH_DELETE"

	EventTypeTenantCreate EventType = "TENANT_CREATE"
	EventTypeTenantUpdate EventType = "TENANT_UPDATE"
	EventTypeTenantDelete EventType = "TENANT_DELETE"

	EventTypeTenantUserCreate EventType = "TENANT_USER_CREATE"
	EventTypeTenantUserUpdate EventType = "TENANT_USER_UPDATE"
	EventTypeTenantUserDelete EventType = "TENANT_USER_DELETE"

	EventTypeAccountCreate EventType = "ACCOUNT_CREATE"
	EventTypeAccountUpdate EventType = "ACCOUNT_UPDATE"
	EventTypeAccountDelete EventType = "ACCOUNT_DELETE"

	EventTypeAutopilotCreate EventType = "AUTOPILOT_CREATE"
	EventTypeAutopilotUpdate EventType = "AUTOPILOT_UPDATE"
	EventTypeAutopilotDelete EventType = "AUTOPILOT_DELETE"

	EventTypeAutorunbookCreate        EventType = "AUTORUNBOOK_CREATE"
	EventTypeAutorunbookUpdate        EventType = "AUTORUNBOOK_UPDATE"
	EventTypeAutorunbookDelete        EventType = "AUTORUNBOOK_DELETE"
	EventTypeAutorunbookManualRun     EventType = "AUTORUNBOOK_MANUAL_RUN"
	EventTypeAutorunbookSkip          EventType = "AUTORUNBOOK_SKIP"
	EventTypeAutorunbookTaskManualRun EventType = "AUTORUNBOOK_TASK_MANUAL_RUN"

	EventTypeRecommendationJobCreate EventType = "RECOMMENDATION_JOB_CREATE"
	EventTypeRecommendationApply     EventType = "RECOMMENDATION_APPLY"
	EventTypeK8sAgentTask            EventType = "K8SAGENT_TASK_CREATE"
	EventTypeK8sRelayTask            EventType = "K8SRELAY_TASK_CREATE"

	EventTypeTicketConfigCreate EventType = "TICKET_CONFIGURATION_CREATE"
	EventTypeTicketConfigUpdate EventType = "TICKET_CONFIGURATION_UPDATE"
	EventTypeTicketConfigDelete EventType = "TICKET_CONFIGURATION_DELETE"

	EventTypeTicketCreate EventType = "TICKET_CREATE"
	EventTypeTicketUpdate EventType = "TICKET_UPDATE"
	EventTypeTicketDelete EventType = "TICKET_DELETE"

	EventTypeNotificationSlackConfigCreate EventType = "NOTIFICATION_SLACK_CONFIGURATION_CREATE"
	EventTypeNotificationSlackConfigUpdate EventType = "NOTIFICATION_SLACK_CONFIGURATION_UPDATE"
	EventTypeNotificationSlackConfigDelete EventType = "NOTIFICATION_SLACK_CONFIGURATION_DELETE"

	EventTypeNotificationSlackChannelCreate EventType = "NOTIFICATION_SLACK_CHANNEL_CREATE"
	EventTypeNotificationSlackChannelUpdate EventType = "NOTIFICATION_SLACK_CHANNEL_UPDATE"
	EventTypeNotificationSlackChannelDelete EventType = "NOTIFICATION_SLACK_CHANNEL_DELETE"

	EventTypeNotificationMsTeamsConfigCreate EventType = "NOTIFICATION_MSTEAMS_CONFIGURATION_CREATE"
	EventTypeNotificationMsTeamsConfigUpdate EventType = "NOTIFICATION_MSTEAMS_CONFIGURATION_UPDATE"
	EventTypeNotificationMsTeamsConfigDelete EventType = "NOTIFICATION_MSTEAMS_CONFIGURATION_DELETE"

	EventTypeNotificationMsTeamsChannelCreate EventType = "NOTIFICATION_MSTEAMS_CHANNEL_CREATE"
	EventTypeNotificationMsTeamsChannelUpdate EventType = "NOTIFICATION_MSTEAMS_CHANNEL_UPDATE"
	EventTypeNotificationMsTeamsChannelDelete EventType = "NOTIFICATION_MSTEAMS_CHANNEL_DELETE"

	EventTypeAlertManagerCreate  EventType = "ALERT_MANAGER_CREATE"
	EventTypeAlertManagerUpdate  EventType = "ALERT_MANAGER_UPDATE"
	EventTypeAlertManagerDisable EventType = "ALERT_MANAGER_DISABLE"
)

type EventActor string

const (
	EventActorApiService          EventActor = "API_SERVICE"
	EventActorMlService           EventActor = "ML_SERVICE"
	EventActorAutopilotService    EventActor = "AUTOPILOT_SERVICE"
	EventActorAutorunbookService  EventActor = "AUTORUNBOOK_SERVICE"
	EventActorNotificationService EventActor = "NOTIFICATION_SERVICE"
	EventActorTicketService       EventActor = "TICKET_SERVICE"
	EventActorK8sAgent            EventActor = "K8S_AGENT"
	EventActorK8sCollectorService EventActor = "K8S_COLLECTOR_SERVICE"
	EventActorUiService           EventActor = "UI_SERVICE"
)

type EventAction string

const (
	EventActionCreate EventAction = "CREATE"
	EventActionUpdate EventAction = "UPDATE"
	EventActionDelete EventAction = "DELETE"
	EventActionRead   EventAction = "READ"
)

type EventStatus string

const (
	EventStatusSuccess EventStatus = "SUCCESS"
	EventStatusFailure EventStatus = "FAILURE"
)

type Audit struct {
	Id             string         `json:"id" db:"id"`
	UserId         string         `json:"user_id" db:"user_id" validate:"omitempty,uuid4"`
	TenantId       string         `json:"tenant_id" db:"tenant_id" validate:"omitempty,uuid4"`
	AccountId      string         `json:"account_id" db:"account_id" validate:"omitempty,uuid4"`
	EventTime      time.Time      `json:"event_time" db:"event_time" validate:"required"`
	EventCategory  EventCategory  `json:"event_category" db:"event_category" validate:"required"`
	EventType      EventType      `json:"event_type" db:"event_type" validate:"required"`
	EventPrevState any            `json:"event_prev_state" db:"event_prev_state"`
	EventState     any            `json:"event_state" db:"event_state" validate:"required"`
	EventActor     EventActor     `json:"event_actor" db:"event_actor" validate:"required"`
	EventTarget    string         `json:"event_target" db:"event_target" validate:"required"`
	EventAction    EventAction    `json:"event_action" db:"event_action" validate:"required"`
	EventStatus    EventStatus    `json:"event_status" db:"event_status" validate:"required"`
	TransactionId  string         `json:"transaction_id" db:"transaction_id" validate:"omitempty"`
	EventAttr      map[string]any `json:"event_attr" db:"event_attr"`
}

type AuditRequest struct {
	Audits []Audit `json:"audits"`
}
