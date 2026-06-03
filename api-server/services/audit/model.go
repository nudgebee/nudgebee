package audit

import "time"

type EventCategory string

const (
	EventCategoryUser              EventCategory = "USERS"
	EventCategoryGroup             EventCategory = "GROUPS"
	EventCategoryRole              EventCategory = "ROLES"
	EventCategoryTenant            EventCategory = "TENANTS"
	EventCategoryAccount           EventCategory = "ACCOUNTS"
	EventCategoryRecommendation    EventCategory = "RECOMMENDATIONS"
	EventCategoryAutopilot         EventCategory = "AUTO_PILOT"
	EventCategoryAutorunbook       EventCategory = "AUTO_RUNBOOK"
	EventCategoryTickets           EventCategory = "TICKETS"
	EventCategoryNotifications     EventCategory = "NOTIFICATIONS"
	EventCategoryK8sAgent          EventCategory = "K8S_AGENT"
	EventCategoryK8sRelay          EventCategory = "K8S_RELAY"
	EventAlertManagerRelay         EventCategory = "ALERT_MANAGER"
	EventAlertEvent                EventCategory = "EVENTS"
	EventCategoryNotificationRules EventCategory = "NOTIFICATION_RULES"
	EventChatActions               EventCategory = "NOTIFICATIONS_CHAT_ACTIONS"
	EventAgentToken                EventCategory = "AGENT_TOKEN"
	EventCategoryIntegration       EventCategory = "INTEGRATIONS"
	EventCategoryTriage            EventCategory = "TRIAGE"
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

	EventTypeTenantAttributeUpsert EventType = "TENANT_USER_ATTRIBUTE_UPSERT"
	EventTypeTenantFeature         EventType = "TENANT_FEATURE"

	EventTypeTenantUserCreate EventType = "TENANT_USER_CREATE"
	EventTypeTenantUserUpdate EventType = "TENANT_USER_UPDATE"
	EventTypeTenantUserDelete EventType = "TENANT_USER_DELETE"

	EventTypeAccountCreate EventType = "ACCOUNT_CREATE"
	EventTypeAccountUpdate EventType = "ACCOUNT_UPDATE"
	EventTypeAccountDelete EventType = "ACCOUNT_DELETE"

	EventTypeAutopilotCreate EventType = "AUTOPILOT_CREATE"
	EventTypeAutopilotUpdate EventType = "AUTOPILOT_UPDATE"
	EventTypeAutopilotDelete EventType = "AUTOPILOT_DELETE"

	EventTypeAutorunbookCreate             EventType = "AUTORUNBOOK_CREATE"
	EventTypeAutorunbookUpdate             EventType = "AUTORUNBOOK_UPDATE"
	EventTypeAutorunbookDelete             EventType = "AUTORUNBOOK_DELETE"
	EventTypeAutorunbookActionUpdate       EventType = "AUTORUNBOOK_ACTION_UPDATE"
	EventTypeAutorunbookActionCreate       EventType = "AUTORUNBOOK_ACTION_CREATE"
	EventTypeAutorunbookActionPublish      EventType = "AUTORUNBOOK_ACTION_PUBLISH"
	EventTypeAutorunbookManualRun          EventType = "AUTORUNBOOK_MANUAL_RUN"
	EventTypeAutorunbookSkip               EventType = "AUTORUNBOOK_SKIP"
	EventTypeAutorunbookTaskManualRun      EventType = "AUTORUNBOOK_TASK_MANUAL_RUN"
	EventTypeAutorunbookWebhookTrigger     EventType = "AUTORUNBOOK_WEBHOOK_TRIGGER"
	EventTypeAutorunbookActionStatusUpdate EventType = "AUTORUNBOOK_ACTION_STATUS_UPDATE"
	EventTypeAutorunbookActionExecute      EventType = "AUTORUNBOOK_ACTION_EXECUTE"

	EventTypeRecommendationJobCreate EventType = "RECOMMENDATION_JOB_CREATE"
	EventTypeRecommendationApply     EventType = "RECOMMENDATION_APPLY"

	EventTypeEventResolve     EventType = "EVENT_RESOLVE"
	EventTypeEventUpdate      EventType = "EVENT_UPDATE"
	EventTypeEventInvestigate EventType = "EVENT_INVESTIGATE"

	EventTypeK8sAgentTask EventType = "K8SAGENT_TASK_CREATE"
	EventTypeK8sRelayTask EventType = "K8SRELAY_TASK_CREATE"

	EventTypeTicketConfigCreate EventType = "TICKET_CONFIGURATION_CREATE"
	EventTypeTicketConfigUpdate EventType = "TICKET_CONFIGURATION_UPDATE"
	EventTypeTicketConfigDelete EventType = "TICKET_CONFIGURATION_DELETE"

	EventTypeTicketCreate EventType = "TICKET_CREATE"
	EventTypeTicketUpdate EventType = "TICKET_UPDATE"
	EventTypeTicketDelete EventType = "TICKET_DELETE"

	EventTypeMessagingPlatformCreate EventType = "MESSAGING_PLATFORM_CREATE"
	EventTypeMessagingPlatformUpdate EventType = "MESSAGING_PLATFORM_UPDATE"
	EventTypeMessagingPlatformDelete EventType = "MESSAGING_PLATFORM_DELETE"

	EventTypeNotificationSlackConfigCreate EventType = "NOTIFICATION_SLACK_CONFIGURATION_CREATE"
	EventTypeNotificationSlackConfigUpdate EventType = "NOTIFICATION_SLACK_CONFIGURATION_UPDATE"
	EventTypeNotificationSlackConfigDelete EventType = "NOTIFICATION_SLACK_CONFIGURATION_DELETE"

	EventTypeNotificationSlackChannelCreate EventType = "NOTIFICATION_SLACK_CHANNEL_CREATE"
	EventTypeNotificationSlackChannelUpdate EventType = "NOTIFICATION_SLACK_CHANNEL_UPDATE"
	EventTypeNotificationSlackChannelDelete EventType = "NOTIFICATION_SLACK_CHANNEL_DELETE"

	EventTypeNotificationMsTeamsConfigCreate EventType = "NOTIFICATION_MS_TEAMS_CONFIGURATION_CREATE"
	EventTypeNotificationMsTeamsConfigUpdate EventType = "NOTIFICATION_MS_TEAMS_CONFIGURATION_UPDATE"
	EventTypeNotificationMsTeamsConfigDelete EventType = "NOTIFICATION_MS_TEAMS_CONFIGURATION_DELETE"

	EventTypeNotificationMsTeamsChannelCreate EventType = "NOTIFICATION_MS_TEAMS_CONFIGURATION_CREATE"
	EventTypeNotificationMsTeamsChannelUpdate EventType = "NOTIFICATION_MS_TEAMS_CHANNEL_UPDATE"
	EventTypeNotificationMsTeamsChannelDelete EventType = "NOTIFICATION_MS_TEAMS_CHANNEL_DELETE"

	EventTypeNotificationGoogleChatConfigCreate EventType = "NOTIFICATION_GOOGLE_CHAT_CONFIGURATION_CREATE"
	EventTypeNotificationGoogleChatConfigUpdate EventType = "NOTIFICATION_GOOGLE_CHAT_CONFIGURATION_UPDATE"
	EventTypeNotificationGoogleChatConfigDelete EventType = "NOTIFICATION_GOOGLE_CHAT_CONFIGURATION_DELETE"

	EventTypeNotificationGoogleChatChannelCreate EventType = "NOTIFICATION_GOOGLE_CHAT_CHANNEL_CREATE"
	EventTypeNotificationGoogleChatChannelUpdate EventType = "NOTIFICATION_GOOGLE_CHAT_CHANNEL_UPDATE"
	EventTypeNotificationGoogleChatChannelDelete EventType = "NOTIFICATION_GOOGLE_CHAT_CHANNEL_DELETE"

	EventTypeNotificationRulesCreate EventType = "NOTIFICATION_RULE_CREATE"
	EventTypeNotificationRulesUpdate EventType = "NOTIFICATION_RULE_UPDATE"
	EventTypeNotificationRulesDelete EventType = "NOTIFICATION_RULE_DELETE"

	EventTypeAlertManagerCreate  EventType = "ALERT_MANAGER_CREATE"
	EventTypeAlertManagerUpdate  EventType = "ALERT_MANAGER_UPDATE"
	EventTypeAlertManagerDisable EventType = "ALERT_MANAGER_DISABLE"

	EventTypeSLOCreate EventType = "SLO_CREATE"
	EventTypeSLOUpdate EventType = "SLO_UPDATE"
	EventTypeSLODelete EventType = "SLO_DELETE"

	EventTypeSlackCommand     EventType = "NOTIFICATIONS_SLACK_COMMAND"
	EventTypeSlackEvent       EventType = "NOTIFICATIONS_SLACK_EVENT"
	EventTypeSlackInteraction EventType = "NOTIFICATIONS_SLACK_INTERACTION"

	EventTypeUpdateAgentToken EventType = "UPDATE_AGENT_TOKEN"

	EventTypeIntegrationCreate EventType = "INTEGRATION_CREATE"
	EventTypeIntegrationDelete EventType = "INTEGRATION_DELETE"
	EventTypeIntegrationUpdate EventType = "INTEGRATION_UPDATE"

	EventTypeTriageClassify     EventType = "TRIAGE_CLASSIFY"
	EventTypeTriageRuleCreate   EventType = "TRIAGE_RULE_CREATE"
	EventTypeTriageRuleUpdate   EventType = "TRIAGE_RULE_UPDATE"
	EventTypeTriageRuleDelete   EventType = "TRIAGE_RULE_DELETE"
	EventTypeTriageStatusUpdate EventType = "TRIAGE_STATUS_UPDATE"
	EventTypeTriageBackfill     EventType = "TRIAGE_BACKFILL"

	EventTypeTenantOnboardingCreate EventType = "TENANT_ONBOARDING_CREATE"
	EventTypeTenantOnboardingUpdate EventType = "TENANT_ONBOARDING_UPDATE"
	EventTypeTenantOnboardingDelete EventType = "TENANT_ONBOARDING_DELETE"
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
	EventActorSlack               EventActor = "NOTIFICATIONS_SLACK"
	EventActorMsTeams             EventActor = "NOTIFICATIONS_MS_TEAMS"
	EventActotGoogleChat          EventActor = "NOTIFICATIONS_GOOGLE_CHAT"
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
	Id             string         `json:"id,omitempty" db:"id"`
	UserId         string         `json:"user_id,omitempty" db:"user_id" validate:"omitempty,uuid4"`
	TenantId       string         `json:"tenant_id,omitempty" db:"tenant_id" validate:"omitempty,uuid4"`
	AccountId      string         `json:"account_id,omitempty" db:"account_id" validate:"omitempty,uuid4"`
	EventTime      time.Time      `json:"event_time,omitempty" db:"event_time" validate:"required"`
	EventCategory  EventCategory  `json:"event_category,omitempty" db:"event_category" validate:"required"`
	EventType      EventType      `json:"event_type,omitempty" db:"event_type" validate:"required"`
	EventPrevState any            `json:"event_prev_state,omitempty" db:"event_prev_state"`
	EventState     any            `json:"event_state,omitempty" db:"event_state" validate:"required"`
	EventActor     EventActor     `json:"event_actor,omitempty" db:"event_actor" validate:"required"`
	EventTarget    string         `json:"event_target,omitempty" db:"event_target" validate:"required"`
	EventAction    EventAction    `json:"event_action,omitempty" db:"event_action" validate:"required"`
	EventStatus    EventStatus    `json:"event_status,omitempty" db:"event_status" validate:"required"`
	TransactionId  string         `json:"transaction_id,omitempty" db:"transaction_id" validate:"omitempty"`
	EventAttr      map[string]any `json:"event_attr" db:"event_attr"`
}

type AuditRequest struct {
	Audits []Audit `json:"audits"`
}
