package ticket

// ticketManagers provides a registry for ticket managers by tool name.
// This enables dynamic dispatch to the appropriate manager based on integration type.
var ticketManagers = make(map[string]TicketManager)

// RegisterTicketManager registers a TicketManager implementation for a tool.
func RegisterTicketManager(toolName string, manager TicketManager) {
	ticketManagers[toolName] = manager
}

// GetTicketManager retrieves the registered TicketManager for a tool.
func GetTicketManager(toolName string) (TicketManager, bool) {
	manager, ok := ticketManagers[toolName]
	return manager, ok
}

// incidentManagers provides a registry for incident managers by tool name.
var incidentManagers = make(map[string]IncidentManager)

// RegisterIncidentManager registers an IncidentManager implementation for a tool.
func RegisterIncidentManager(toolName string, manager IncidentManager) {
	incidentManagers[toolName] = manager
	// Also register as a TicketManager since IncidentManager embeds it
	ticketManagers[toolName] = manager
}

// GetIncidentManager retrieves the registered IncidentManager for a tool.
func GetIncidentManager(toolName string) (IncidentManager, bool) {
	manager, ok := incidentManagers[toolName]
	return manager, ok
}
