package agents

// The Datadog agents run for another account: every datadog_*_execute tool
// follows a handed target and picks the target account's own Datadog
// integration, as postgres does. metrics and traces hand a Datadog account's
// question to datadog_metrics and datadog_traces with the target attached.
func (DatadogContainersAgent) AcceptsExecutionTarget()      {}
func (DatadogContainersQueryAgent) AcceptsExecutionTarget() {}
func (DatadogEventsAgent) AcceptsExecutionTarget()          {}
func (DatadogHostsAgent) AcceptsExecutionTarget()           {}
func (DatadogIncidentAgent) AcceptsExecutionTarget()        {}
func (DatadogMetricsAgent) AcceptsExecutionTarget()         {}
func (DatadogMetricsQueryAgent) AcceptsExecutionTarget()    {}
func (*DatadogOrchestratorAgent) AcceptsExecutionTarget()   {}
func (DatadogServiceAgent) AcceptsExecutionTarget()         {}
func (DatadogSoftwareCatalogAgent) AcceptsExecutionTarget() {}
func (DatadogTracesAgent) AcceptsExecutionTarget()          {}
