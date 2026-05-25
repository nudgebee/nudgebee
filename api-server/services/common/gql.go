package common

// GqlResponse and GqlResponseError preserve the JSON wire shape that
// downstream notification templates consume from report payloads. The types
// are retained even though no GraphQL transport remains in this package.

type GqlResponseError struct {
	Message    string `json:"message"`
	Extensions any    `json:"extensions"`
}

func (e GqlResponseError) Error() string {
	return e.Message
}

type GqlResponse struct {
	Data   map[string]any     `json:"data"`
	Errors []GqlResponseError `json:"errors"`
}
