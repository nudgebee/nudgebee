package tools

import (
	"embed"
	"fmt"
	"nudgebee/llm/common"
)

type Action struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Actions     []Action `json:"actions,omitempty"` // Nested actions
}

type Toolset struct {
	Name    string   `json:"name"`
	Actions []Action `json:"actions"`
}

type ToolsetsData struct {
	Toolsets []Toolset `json:"toolsets"`
}

//go:embed actions.json
var actionsFS embed.FS

var toolSetData ToolsetsData
var toolSetDataLoaded = false

func GetActions(category string) (ToolsetsData, error) {

	if !toolSetDataLoaded {
		actions, err := actionsFS.ReadFile("actions.json")
		if err != nil {
			fmt.Println("Error opening file:", err)
			return ToolsetsData{}, err
		}
		// Unmarshal the JSON data into the struct
		var toolsetsData1 ToolsetsData
		err = common.UnmarshalJson(actions, &toolsetsData1)
		if err != nil {
			fmt.Println("Error unmarshaling JSON:", err)
			return ToolsetsData{}, err
		}
		if category != "" {
			var filteredToolsets []Toolset
			for _, toolset := range toolsetsData1.Toolsets {
				if toolset.Name == category {
					filteredToolsets = append(filteredToolsets, toolset)
				}
			}
			toolsetsData1.Toolsets = filteredToolsets
		}
		toolSetDataLoaded = true
		toolSetData = toolsetsData1
	}

	return toolSetData, nil
}
