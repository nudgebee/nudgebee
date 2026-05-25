package events

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"

	"nudgebee/runbook/common"
	"nudgebee/runbook/config"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// investigationCompletedEnvelope mirrors the shape llm-server's
// publishInvestigationCompleted emits. Field names are coupled by JSON
// tag; if either side changes, both must move together.
type investigationCompletedEnvelope struct {
	TaskToken     string `json:"task_token"`
	EventID       string `json:"event_id"`
	AccountID     string `json:"account_id"`
	Status        string `json:"status"`
	Summary       string `json:"summary,omitempty"`
	LogSummary    string `json:"log_summary,omitempty"`
	LogAnalysis   string `json:"log_analysis,omitempty"`
	Investigation string `json:"investigation,omitempty"`
	StatusReason  string `json:"status_reason,omitempty"`
	Error         string `json:"error,omitempty"`
}

// investigationStatusFailed marks a terminal-failed analysis. Matches
// llm-server's events.AnalysisStatusFailed string value.
const investigationStatusFailed = "FAILED"

// InvestigationCompletionConsumer resumes Temporal activities suspended
// by llm.event_investigate. llm-server publishes one envelope per
// originating workflow (each carrying a distinct TaskToken); this
// consumer decodes the token and calls CompleteActivity so the workflow
// proceeds with the investigation result.
type InvestigationCompletionConsumer struct {
	temporalClient client.Client
	logger         *slog.Logger
}

// NewInvestigationCompletionConsumer wires the consumer with the running
// Temporal client. The client is shared with the workflow service so
// CompleteActivity targets the same Temporal namespace as the activities
// being resumed.
func NewInvestigationCompletionConsumer(tc client.Client, logger *slog.Logger) *InvestigationCompletionConsumer {
	return &InvestigationCompletionConsumer{temporalClient: tc, logger: logger}
}

// Start subscribes to the configured completion exchange. Returns nil
// when the configuration is incomplete so a missing config doesn't block
// runbook-server boot — the workflow will simply time out via
// StartToCloseTimeout if completions never arrive.
func (c *InvestigationCompletionConsumer) Start() error {
	exch := config.Config.RabbitMqEventInvestigateCompletedExchange
	rk := config.Config.RabbitMqEventInvestigateCompletedRoutingKey
	queue := config.Config.RabbitMqEventInvestigateCompletedQueue
	if exch == "" || queue == "" || rk == "" {
		c.logger.Warn("investigation completion consumer: exchange/queue/routing-key not configured, not starting")
		return nil
	}
	c.logger.Info("starting investigation completion consumer", "exchange", exch, "queue", queue)
	return common.MqConsume(exch, rk, queue, c.processMessage)
}

// processMessage decodes the envelope and resumes (or fails) the
// suspended activity. Returns nil on every code path so messages are
// always ack'd — a hung activity is preferable to an MQ redelivery
// storm. Failures are logged and surface in metrics / dashboards.
func (c *InvestigationCompletionConsumer) processMessage(data []byte) error {
	var env investigationCompletedEnvelope
	if err := common.UnmarshalJson(data, &env); err != nil {
		c.logger.Error("investigation completion: failed to decode envelope", "error", err, "data", string(data))
		return nil
	}
	if env.TaskToken == "" {
		c.logger.Warn("investigation completion: empty task_token, dropping", "event_id", env.EventID)
		return nil
	}

	tokenBytes, err := base64.StdEncoding.DecodeString(env.TaskToken)
	if err != nil {
		c.logger.Error("investigation completion: invalid base64 task_token, dropping", "error", err, "event_id", env.EventID)
		return nil
	}

	ctx := context.Background()

	if env.Status == investigationStatusFailed {
		errMsg := env.Error
		if errMsg == "" {
			errMsg = env.StatusReason
		}
		if errMsg == "" {
			errMsg = "investigation failed without specific error"
		}
		c.logger.Info("investigation completion: failing activity", "event_id", env.EventID, "error", errMsg)
		if err := c.temporalClient.CompleteActivity(ctx, tokenBytes, nil, errors.New(errMsg)); err != nil {
			c.logActivityError("CompleteActivity (failure)", err, env.EventID)
		}
		return nil
	}

	// Output keys must match LLMEventInvestigateTask.OutputSchema.
	output := map[string]any{
		"data":          env.Summary,
		"log_summary":   env.LogSummary,
		"log_analysis":  env.LogAnalysis,
		"investigation": env.Investigation,
		"status":        env.Status,
		"status_reason": env.StatusReason,
	}
	c.logger.Info("investigation completion: resuming activity", "event_id", env.EventID, "status", env.Status)
	if err := c.temporalClient.CompleteActivity(ctx, tokenBytes, output, nil); err != nil {
		c.logActivityError("CompleteActivity", err, env.EventID)
	}
	return nil
}

// logActivityError downgrades NotFound to info — that just means the
// activity already terminated (most often via StartToCloseTimeout while
// the analysis was running longer than the workflow allowed). Anything
// else stays at warn so it shows up in dashboards.
func (c *InvestigationCompletionConsumer) logActivityError(op string, err error, eventID string) {
	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) {
		c.logger.Info("investigation completion: activity already terminated, ignoring", "op", op, "event_id", eventID)
		return
	}
	c.logger.Warn("investigation completion: temporal returned error", "op", op, "error", err, "event_id", eventID)
}
