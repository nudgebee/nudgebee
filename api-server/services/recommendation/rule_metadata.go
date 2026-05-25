package recommendation

import (
	"nudgebee/services/internal/database"
	"nudgebee/services/internal/database/models"
)

type RuleMetadata struct {
	RuleName        string      `json:"rule_name" db:"rule_name"`
	Category        string      `json:"category" db:"category"`
	Title           string      `json:"title" db:"title"`
	Description     *string     `json:"description" db:"description"`
	ServiceName     *string     `json:"service_name" db:"service_name"`
	Recommendations models.Json `json:"recommendations" db:"recommendations"`
	Mitigations     models.Json `json:"mitigations" db:"mitigations"`
	Compliances     models.Json `json:"compliances" db:"compliances"`
	References      models.Json `json:"references" db:"references"`
}

func GetRuleMetadataByName(ruleName string) (*RuleMetadata, error) {
	return GetRuleMetadataByNameAndProvider(ruleName, "")
}

func GetRuleMetadataByNameAndProvider(ruleName, cloudProvider string) (*RuleMetadata, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	var m RuleMetadata
	if cloudProvider != "" {
		err = dbm.Db.QueryRowx("SELECT rule_name, category, title, description, service_name, recommendations, mitigations, compliances, \"references\" FROM recommendation_rule WHERE rule_name = $1 AND (cloud_provider = $2 OR cloud_provider IS NULL) ORDER BY cloud_provider DESC NULLS LAST LIMIT 1", ruleName, cloudProvider).StructScan(&m)
	} else {
		err = dbm.Db.QueryRowx("SELECT rule_name, category, title, description, service_name, recommendations, mitigations, compliances, \"references\" FROM recommendation_rule WHERE rule_name = $1 ORDER BY cloud_provider NULLS FIRST LIMIT 1", ruleName).StructScan(&m)
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func GetAllRuleMetadataByCategory(category string) ([]RuleMetadata, error) {
	dbm, err := database.GetDatabaseManager(database.Metastore)
	if err != nil {
		return nil, err
	}
	var results []RuleMetadata
	if category != "" {
		err = dbm.Db.Select(&results, "SELECT rule_name, category, title, description, service_name, recommendations, mitigations, compliances, \"references\" FROM recommendation_rule WHERE category = $1 AND cloud_provider IS NULL", category)
	} else {
		err = dbm.Db.Select(&results, "SELECT rule_name, category, title, description, service_name, recommendations, mitigations, compliances, \"references\" FROM recommendation_rule WHERE cloud_provider IS NULL")
	}
	return results, err
}
