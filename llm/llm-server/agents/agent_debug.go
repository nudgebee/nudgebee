package agents

import (
	"fmt"
	"nudgebee/llm/agents/aws"
	"nudgebee/llm/common"
	"strings"
	"sync"
)

var accountDebugAgentMap = map[string]string{}
var accountDebugAgentMapMutex sync.RWMutex

func GetDebugAgentName(accountId string) string {
	accountDebugAgentMapMutex.RLock()
	plannerAgent := accountDebugAgentMap[accountId]
	accountDebugAgentMapMutex.RUnlock()

	defaultAgentName := AgentK8sDebugName

	if plannerAgent == "" {
		plannerAgent = defaultAgentName
		// use DB to get account type
		dbms, err := common.GetDatabaseManager(common.Metastore)
		if err != nil {
			return defaultAgentName
		}
		rows, err := dbms.Query("select cloud_provider from cloud_accounts where id = $1", accountId)
		if err != nil {
			return defaultAgentName
		}
		defer func() {
			if err := rows.Close(); err != nil {
				// Log the error, but don't return it as it's a defer call
				fmt.Printf("Error closing rows: %v\n", err)
			}
		}()
		for rows.Next() {
			var cloudProvider string
			err = rows.Scan(&cloudProvider)
			if err != nil {
				return defaultAgentName
			}
			planner := defaultAgentName
			if strings.EqualFold(cloudProvider, "aws") {
				planner = aws.AgentAwsDebugName
			} else if strings.EqualFold(cloudProvider, "gcp") {
				planner = AgentGcpDebugName
			} else if strings.EqualFold(cloudProvider, "azure") {
				planner = AgentAzureDebugName
			}
			accountDebugAgentMapMutex.Lock()
			accountDebugAgentMap[accountId] = planner
			accountDebugAgentMapMutex.Unlock()
			plannerAgent = planner
		}
	}
	if plannerAgent == "" {
		plannerAgent = defaultAgentName
	}
	return plannerAgent
}
