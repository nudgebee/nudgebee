package spend

type GetSpendByCloudAccountResponse struct {
	Id               string  `json:"id" mapstructure:"id" db:"id"`
	AccountName      string  `json:"account_name" mapstructure:"account_name" db:"account_name"`
	Amount           float32 `json:"amount" mapstructure:"amount" db:"amount"`
	Saving           float32 `json:"saving" mapstructure:"saving" db:"saving"`
	AmountLast       float32 `json:"amount_last" mapstructure:"amount_last" db:"amount_last"`
	PercentageChange float32 `json:"percentage_change" mapstructure:"percentage_change" db:"percentage_change"`
}

type GetSpendByServiceResponse struct {
	Tenant                  string  `json:"tenant" mapstructure:"tenant" db:"tenant" validate:"required"`
	ServiceName             string  `json:"service_name" mapstructure:"service_name" db:"service_name" validate:"required"`
	ResourceCount           int     `json:"resource_count" mapstructure:"resource_count" db:"resource_count"`
	SpendAmount             float32 `json:"spend_amount" mapstructure:"spend_amount" db:"spend_amount"`
	SpendAmountLast         float32 `json:"spend_amount_last" mapstructure:"spend_amount_last" db:"spend_amount_last"`
	PercentageChange        float32 `json:"percentage_change" mapstructure:"percentage_change" db:"percentage_change"`
	ResourceEstimatedSaving float32 `json:"resource_estimated_saving" mapstructure:"resource_estimated_saving" db:"resource_estimated_saving"`
}
