package account

// AwsOrgOnboardRequest is the request payload for initiating AWS Organization onboarding.
type AwsOrgOnboardRequest struct {
	AccountName string `json:"account_name" mapstructure:"account_name" validate:"required"`
}

// AwsOrgOnboardResponse is returned after initiating org onboarding.
// It contains the verification token (shown once), the StackSet template URL,
// a launch URL for the AWS StackSets console, and parameter values for StackSet creation.
type AwsOrgOnboardResponse struct {
	VerificationToken   string            `json:"verification_token"`
	StackSetTemplateUrl string            `json:"stackset_template_url"`
	StackSetLaunchUrl   string            `json:"stackset_launch_url"`
	SnsTopicArn         string            `json:"sns_topic_arn"`
	StackSetParameters  map[string]string `json:"stackset_parameters"`
}

// AwsOrgStatusResponse contains the status of an AWS Organization and its member accounts.
type AwsOrgStatusResponse struct {
	OrgName        string            `json:"org_name"`
	OrgStatus      string            `json:"org_status"`
	MemberAccounts []OrgMemberStatus `json:"member_accounts"`
}

// OrgMemberStatus represents a single member account registered via org onboarding.
type OrgMemberStatus struct {
	AccountId     string `json:"account_id" db:"id"`
	AccountNumber string `json:"account_number" db:"account_number"`
	AccountName   string `json:"account_name" db:"account_name"`
	Status        string `json:"status" db:"status"`
	CreatedAt     string `json:"created_at" db:"created_at"`
}

// AwsOrgRefreshTokenResponse is returned after regenerating the org verification token.
type AwsOrgRefreshTokenResponse struct {
	VerificationToken string `json:"verification_token"`
}
