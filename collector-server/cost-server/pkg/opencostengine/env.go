package opencostengine

import (
	"os"
	"strings"
)

// GetNudgebeeDbConnectionString returns the Postgres connection string.
func GetNudgebeeDbConnectionString() string {
	return os.Getenv("NUDGEBEE_DB")
}

func GetNudgebeeRelayEndpoint() string {
	endpoint := os.Getenv("RELAY_SERVER_ENDPOINT")
	endpoint = strings.TrimSuffix(endpoint, "/")
	return endpoint
}

func GetNudgebeeRelayAuthToken() string {
	return os.Getenv("RELAY_SERVER_SECRET_KEY")
}

// GetClusterInfoProvider returns the cluster info provider backend to use.
func GetClusterInfoProvider() string {
	return os.Getenv("CLOUD_COST_PROVIDER")
}
