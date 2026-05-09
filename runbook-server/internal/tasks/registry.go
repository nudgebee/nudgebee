package tasks

import (
	"fmt"
	"nudgebee/runbook/common"
	"nudgebee/runbook/internal/tasks/ai"
	"nudgebee/runbook/internal/tasks/aws"
	"nudgebee/runbook/internal/tasks/azure"
	"nudgebee/runbook/internal/tasks/cicd"
	"nudgebee/runbook/internal/tasks/cloud"
	"nudgebee/runbook/internal/tasks/core"
	"nudgebee/runbook/internal/tasks/crypto"
	"nudgebee/runbook/internal/tasks/data"
	"nudgebee/runbook/internal/tasks/dbms"
	"nudgebee/runbook/internal/tasks/events"
	"nudgebee/runbook/internal/tasks/gcp"
	"nudgebee/runbook/internal/tasks/integrations"
	"nudgebee/runbook/internal/tasks/k8s"
	"nudgebee/runbook/internal/tasks/mq"
	"nudgebee/runbook/internal/tasks/network"
	"nudgebee/runbook/internal/tasks/notifications"
	"nudgebee/runbook/internal/tasks/notifications/slack"
	"nudgebee/runbook/internal/tasks/observability"
	"nudgebee/runbook/internal/tasks/scm"
	"nudgebee/runbook/internal/tasks/scripting"
	"nudgebee/runbook/internal/tasks/system"
	"nudgebee/runbook/internal/tasks/tickets"
	"nudgebee/runbook/internal/tasks/types"
)

// TaskRegistry holds the mapping from task names to task implementations.
type TaskRegistry struct {
	tasks map[string]types.Task
}

// NewTaskRegistry creates and returns a new, empty TaskRegistry.
func NewTaskRegistry() *TaskRegistry {
	return &TaskRegistry{
		tasks: make(map[string]types.Task),
	}
}

// Register adds a new task to the registry using its name as the key.
func (tr *TaskRegistry) RegisterTask(task types.Task) {
	name := task.GetName()
	tr.tasks[name] = task
}

// Get retrieves a task implementation by its name.
func (tr *TaskRegistry) GetTask(name string) (types.Task, error) {
	task, ok := tr.tasks[name]
	if !ok {
		return nil, common.ErrorNotFound(fmt.Sprintf("task not found: %s", name))
	}
	return task, nil
}

// ListTasks returns a slice of all registered tasks.
func (tr *TaskRegistry) ListTasks() []types.Task {
	var tasks []types.Task
	for _, task := range tr.tasks {
		tasks = append(tasks, task)
	}
	return tasks
}

// ListAutoExecutableTasks returns a slice of all registered tasks that implement TaskAutoExecute.
func (tr *TaskRegistry) ListAutoExecutableTasks() []types.TaskAutoExecute {
	var autoExecutableTasks []types.TaskAutoExecute
	for _, task := range tr.tasks {
		if autoTask, ok := task.(types.TaskAutoExecute); ok {
			autoExecutableTasks = append(autoExecutableTasks, autoTask)
		}
	}
	return autoExecutableTasks
}

func NewInitializedTaskRegistry() *TaskRegistry {
	tr := NewTaskRegistry()

	tr.RegisterTask(&core.ApprovalTask{})
	tr.RegisterTask(&core.GroupTask{})
	tr.RegisterTask(&core.ForEachTask{})
	tr.RegisterTask(&core.PrintTask{})
	tr.RegisterTask(&core.SwitchTask{})
	tr.RegisterTask(&core.WaitTask{})

	tr.RegisterTask(&data.TransformTask{})
	tr.RegisterTask(&data.FilterTask{})
	tr.RegisterTask(&scripting.RunScriptTask{})
	tr.RegisterTask(&integrations.HttpTask{})
	tr.RegisterTask(&integrations.SSHTask{})
	tr.RegisterTask(&notifications.ImSendTask{})
	tr.RegisterTask(&notifications.ReadThreadTask{})
	tr.RegisterTask(&notifications.AddReactionTask{})
	tr.RegisterTask(&notifications.EmailTask{})
	tr.RegisterTask(&notifications.DmSendTask{})
	tr.RegisterTask(&slack.SlackJoinChannelTask{})
	tr.RegisterTask(&tickets.TicketsCreateTask{})
	tr.RegisterTask(&tickets.TicketsAddCommentTask{})
	tr.RegisterTask(&tickets.TicketsGetTask{})
	tr.RegisterTask(&tickets.TicketsGetCommentsTask{})
	tr.RegisterTask(&tickets.TicketsAcknowledgeTask{})
	tr.RegisterTask(&tickets.TicketsEscalateTask{})
	tr.RegisterTask(&tickets.TicketsResolveTask{})
	tr.RegisterTask(&tickets.TicketsUpdateTask{})
	tr.RegisterTask(&tickets.TicketsTransitionTask{})
	tr.RegisterTask(&tickets.TicketsAssignTask{})
	tr.RegisterTask(&ai.LLMSummaryTask{})
	tr.RegisterTask(&ai.LLMNubiTask{})
	tr.RegisterTask(&ai.LLMInvestigateTask{})
	tr.RegisterTask(&ai.LLMEventInvestigateTask{})
	tr.RegisterTask(&ai.RouterTask{})
	tr.RegisterTask(&ai.LLMRouterClassifyTask{})
	tr.RegisterTask(&ai.MCPTask{})
	tr.RegisterTask(&ai.LLMA2ATask{})

	tr.RegisterTask(&cloud.AWSCliTask{})
	tr.RegisterTask(&cloud.AzureCliTask{})
	tr.RegisterTask(&cloud.GCPCliTask{})
	tr.RegisterTask(&cloud.K8sCliTask{})

	tr.RegisterTask(&aws.AWSCliTask{})
	tr.RegisterTask(&azure.AzureCliTask{})
	tr.RegisterTask(&gcp.GCPCliTask{})
	tr.RegisterTask(&k8s.K8sCliTask{})
	tr.RegisterTask(&k8s.VerticalRightsizeTask{})
	tr.RegisterTask(&k8s.HorizontalRightsizeTask{})
	tr.RegisterTask(&k8s.PVRightsizeTask{})
	tr.RegisterTask(&k8s.ContinuousRightsizeTask{})
	tr.RegisterTask(&k8s.PodDeleteTask{})
	tr.RegisterTask(&k8s.WorkloadRestartTask{})
	tr.RegisterTask(&k8s.NodeGracefulShutdownTask{})

	tr.RegisterTask(&system.VerticalRightsizeGenerateTask{})

	tr.RegisterTask(&observability.LogsTask{})
	tr.RegisterTask(&observability.LogGroupsTask{})
	tr.RegisterTask(&observability.MetricsTask{})
	tr.RegisterTask(&observability.TracesTask{})

	tr.RegisterTask(&cicd.ArgoCDCliTask{})
	tr.RegisterTask(&dbms.DBMSQueryTask{})
	tr.RegisterTask(&dbms.RedisCliTask{})
	tr.RegisterTask(&mq.RabbitmqadminCliTask{})
	tr.RegisterTask(&scm.GithubCliTask{})
	tr.RegisterTask(&scm.GitlabCliTask{})
	tr.RegisterTask(&events.EventsStoreTask{})

	tr.RegisterTask(&network.DnsTask{})
	tr.RegisterTask(&network.TcpTask{})
	tr.RegisterTask(&network.SslTask{})
	tr.RegisterTask(&network.PingTask{})
	tr.RegisterTask(&network.WhoisTask{})
	tr.RegisterTask(&network.TracerouteTask{})
	tr.RegisterTask(&network.NtpTask{})

	tr.RegisterTask(&crypto.CryptoEncodeTask{})
	tr.RegisterTask(&crypto.CryptoDecodeTask{})
	tr.RegisterTask(&crypto.CryptoHashTask{})
	tr.RegisterTask(&crypto.CryptoEncryptTask{})
	tr.RegisterTask(&crypto.CryptoDecryptTask{})

	return tr
}
