package tools

import (
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// What each ticket operation does to the ticketing system. An operation not
// listed (or no operation found) asks a person; the tools reject it anyway.
var (
	ticketV1OperationTypes = map[string]core.ToolRequestType{
		OperationSearch:  core.ToolRequestTypeRead,
		OperationUpdate:  core.ToolRequestTypeUpdate,
		OperationComment: core.ToolRequestTypeCreate,
	}
	ticketV2OperationTypes = map[string]core.ToolRequestType{
		v2OpGetCreateMeta: core.ToolRequestTypeRead,
		v2OpGetComments:   core.ToolRequestTypeRead,
		v2OpGetTicket:     core.ToolRequestTypeRead,
		v2OpListTickets:   core.ToolRequestTypeRead,
		v2OpCreateTicket:  core.ToolRequestTypeCreate,
		v2OpAddComment:    core.ToolRequestTypeCreate,
	}
)

// ticketRequestType classifies a ticket tool call by its operation_type. The
// executor hands Call either the `command` string, a `command` object, or the
// whole input, so every place an operation_type can sit is read and the
// strongest wins.
func ticketRequestType(input string, operations map[string]core.ToolRequestType) core.ToolRequestType {
	candidates := []string{input}
	top := map[string]any{}
	if common.UnmarshalJson([]byte(input), &top) == nil {
		switch cmd := top["command"].(type) {
		case string:
			candidates = append(candidates, cmd)
		case map[string]any:
			if v, ok := cmd["value"].(string); ok {
				candidates = append(candidates, v)
			}
			if b, err := common.MarshalJson(cmd); err == nil {
				candidates = append(candidates, string(b))
			}
		}
	}
	result := core.ToolRequestType("")
	for _, candidate := range candidates {
		var op struct {
			OperationType string `json:"operation_type"`
		}
		if common.UnmarshalJson([]byte(candidate), &op) != nil || op.OperationType == "" {
			continue
		}
		requestType, known := operations[strings.TrimSpace(op.OperationType)]
		if !known {
			requestType = core.ToolRequestTypeUpdate
		}
		result = strongerShellRequestType(result, requestType)
	}
	if result == "" {
		return core.ToolRequestTypeUpdate
	}
	return result
}

func (m TicketMaster) InferToolRequestType(_ *security.RequestContext, _, input string) (core.ToolRequestType, error) {
	return ticketRequestType(input, ticketV1OperationTypes), nil
}

// A confirmation covers only this exact ticket change, not every later one.
func (m TicketMaster) ConfirmationKey(toolInput string) string {
	return perActionConfirmationKey(m.Name(), toolInput)
}

func (m TicketMasterV2) InferToolRequestType(_ *security.RequestContext, _, input string) (core.ToolRequestType, error) {
	return ticketRequestType(input, ticketV2OperationTypes), nil
}

// A confirmation covers only this exact ticket or comment, not every later one.
func (m TicketMasterV2) ConfirmationKey(toolInput string) string {
	return perActionConfirmationKey(m.Name(), toolInput)
}
