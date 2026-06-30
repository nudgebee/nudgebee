package opencostengine

import (
	"database/sql"

	"github.com/opencost/opencost/core/pkg/clusters"
	"github.com/opencost/opencost/core/pkg/log"
)

// NudgebeeClusterInfoProvider is a ClusterInfoProvider implementation that uses a database as its backend.
type NudgebeeClusterInfoProvider struct {
	db        *DB
	clusterID string
}

// NewNudgebeeClusterInfoProvider creates a new NudgebeeClusterInfoProvider.
func NewNudgebeeClusterInfoProvider(db *DB, clusterId string) clusters.ClusterInfoProvider {
	return &NudgebeeClusterInfoProvider{
		db:        db,
		clusterID: clusterId,
	}
}

// GetClusterInfo returns a string map containing the local/remote connected cluster info
func (ncip *NudgebeeClusterInfoProvider) GetClusterInfo() map[string]string {
	if ncip.clusterID == "" {
		return map[string]string{}
	}
	query := `
		SELECT id, account_name, cloud_provider, region
		FROM cloud_accounts
		WHERE id = $1 AND cloud_provider = 'K8s' AND status = 'active'
	`

	row := ncip.db.Connection().QueryRow(query, ncip.clusterID)

	var id, name, provider string
	var region *string
	err := row.Scan(&id, &name, &provider, &region)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Warnf("NudgebeeClusterInfoProvider: No active K8s cluster found for id '%s'", ncip.clusterID)
			return map[string]string{}
		}
		log.Errorf("NudgebeeClusterInfoProvider: Error scanning cluster info: %s", err)
		return map[string]string{}
	}

	info := make(map[string]string)
	info[clusters.ClusterInfoIdKey] = id
	info[clusters.ClusterInfoNameKey] = name
	info[clusters.ClusterInfoProviderKey] = provider
	if region != nil {
		info[clusters.ClusterInfoRegionKey] = *region
	}

	return info
}
