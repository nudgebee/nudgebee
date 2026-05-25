package integrations

type IntegrationConfig struct {
	Id      string                   `json:"id"`
	Name    string                   `json:"name"`
	Values  []IntegrationConfigValue `json:"values"`
	Configs []IntegrationConfigValue `json:"configs"`
	Tags    map[string]any           `json:"tags"`
}

type IntegrationConfigValue struct {
	Name        string `json:"name"`
	Value       string `json:"value"`
	IsEncrypted bool   `json:"is_encrypted"`
}
