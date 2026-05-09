package marketplace

import (
	"nudgebee/services/internal/database/models"
	"time"
)

type Customer struct {
	ID                 string      `json:"id" validate:"required" db:"id"`
	CreatedAt          time.Time   `json:"created_at" validate:"required" db:"created_at"`
	UpdatedAt          time.Time   `json:"updated_at" validate:"required" db:"updated_at"`
	CustomerIdentifier string      `json:"customer_identifier" validate:"required" db:"customer_identifier"`
	ProviderAccountId  string      `json:"provider_account_id" validate:"required" db:"provider_account_id"`
	ProductCode        string      `json:"product_code" validate:"required" db:"product_code"`
	PricingTier        string      `json:"pricing_tier" db:"pricing_tier"`
	EntitlementDetails models.Json `json:"entitlement_details" db:"entitlement_details"`
	OfferIdentifier    string      `json:"offer_identifier" db:"offer_identifier"`
	IsFreeTrialOn      bool        `json:"is_free_trial_on" db:"is_free_trial_on"`
	SubscriptionExpiry time.Time   `json:"subscription_expiry" db:"subscription_expiry"`
	Action             string      `json:"action" db:"action"`
	IsActive           bool        `json:"is_active" validate:"required" db:"status"`
	TenantID           string      `json:"tenant_id" db:"tenant_id"`
	Name               string      `json:"name" db:"name"`
	Marketplace        string      `json:"marketplace" validate:"required" db:"marketplace"`
	SubscriptionStatus string      `json:"subscription_status" db:"subscription_status"`
}

type CustomerSubscription struct {
	CustomerIdentifier string `json:"customer_identifier" validate:"required"`
	ProviderAccountId  string `json:"provider_account_id" validate:"required"`
	ProductCode        string `json:"product_code" validate:"required"`
	Marketplace        string `json:"marketplace" validate:"required"`
}

type CustomerTenant struct {
	Id                 string `json:"id" db:"id"`
	CustomerIdentifier string `json:"customer_identifier" db:"customer_identifier" validate:"required"`
	TenantId           any    `json:"tenant_id" db:"tenant_id"`
	Marketplace        string `json:"marketplace" db:"marketplace"`
}

type NewCustomerTenantRequest struct {
	CustomerIdentifier string `json:"customer_identifier" db:"customer_identifier" validate:"required"`
	Username           string `json:"username" mapstructure:"username" validate:"required"`
	Firstname          string `json:"firstname" mapstructure:"firstname" validate:"required"`
	Lastname           string `json:"lastname" mapstructure:"lastname"`
	Tenantname         string `json:"tenantname" mapstructure:"tenantname"`
	Role               string `json:"role" mapstructure:"role"`
}

type SNSMessage struct {
	Type             string `json:"Type"`
	MessageId        string `json:"MessageId"`
	TopicArn         string `json:"TopicArn"`
	Message          string `json:"Message"`
	Timestamp        string `json:"Timestamp"`
	SignatureVersion string `json:"SignatureVersion"`
	Signature        string `json:"Signature"`
	SigningCertURL   string `json:"SigningCertURL"`
	UnsubscribeURL   string `json:"UnsubscribeURL"`
}

type AwsEntitlementPayload struct {
	Action             string `json:"action"`
	CustomerIdentifier string `json:"customer-identifier"`
	ProductCode        string `json:"product-code"`
}

type AwsSubscriptionPayload struct {
	Action                 string `json:"action"`
	CustomerIdentifier     string `json:"customer-identifier"`
	ProductCode            string `json:"product-code"`
	OfferIdentifier        string `json:"offer-identifier"`
	IsFreeTrialTermPresent string `json:"isFreeTrialTermPresent"`
}

type AzureSubscription struct {
	ID                     string    `json:"id"`
	Name                   string    `json:"name"`
	PublisherID            string    `json:"publisherId"`
	OfferID                string    `json:"offerId"`
	PlanID                 string    `json:"planId"`
	Quantity               int       `json:"quantity"`
	Beneficiary            AzureUser `json:"beneficiary"`
	Purchaser              AzureUser `json:"purchaser"`
	AllowedCustomerOps     []string  `json:"allowedCustomerOperations"`
	SessionMode            string    `json:"sessionMode"`
	IsFreeTrial            bool      `json:"isFreeTrial"`
	IsTest                 bool      `json:"isTest"`
	SandboxType            string    `json:"sandboxType"`
	SaasSubscriptionStatus string    `json:"saasSubscriptionStatus"`
	Term                   AzureTerm `json:"term"`
	AutoRenew              bool      `json:"autoRenew"`
	Created                time.Time `json:"created"`
	LastModified           time.Time `json:"lastModified"`
}

type AzureUser struct {
	EmailID  string `json:"emailId"`
	ObjectID string `json:"objectId"`
	TenantID string `json:"tenantId"`
	PUID     string `json:"puid"`
}

type AzureTerm struct {
	StartDate      time.Time `json:"startDate"`
	EndDate        time.Time `json:"endDate"`
	TermUnit       string    `json:"termUnit"`
	ChargeDuration *string   `json:"chargeDuration"`
}

type AzurePayload struct {
	ID                     string            `json:"id"`
	ActivityID             string            `json:"activityId"`
	PublisherID            string            `json:"publisherId"`
	OfferID                string            `json:"offerId"`
	PlanID                 string            `json:"planId"`
	Quantity               int               `json:"quantity"`
	SubscriptionID         string            `json:"subscriptionId"`
	TimeStamp              time.Time         `json:"timeStamp"`
	Action                 string            `json:"action"`
	Status                 string            `json:"status"`
	OperationRequestSource string            `json:"operationRequestSource"`
	Subscription           AzureSubscription `json:"subscription"`
	PurchaseToken          *string           `json:"purchaseToken"`
}

type AzureUsageEvent struct {
	ResourceID         string    `json:"resourceId"`
	Quantity           float64   `json:"quantity"`
	Dimension          string    `json:"dimension"`
	EffectiveStartTime time.Time `json:"effectiveStartTime"`
	PlanID             string    `json:"planId"`
}

type AzureUsageEventRequest struct {
	UsageEvents []AzureUsageEvent `json:"request"`
}

type TestMeteredUsageRequest struct {
	CustomerIdentifier string `json:"customer_identifier" validate:"required"`
	ProductCode        string `json:"product_code" validate:"required"`
	Dimension          string `json:"dimension" validate:"required"`
	Quantity           int32  `json:"quantity" validate:"required"`
}
