// Package agentconnect runs the work a K8s agent's first connect kicks off — the
// initial recommendation scans and the server-side OpenCost sync — on the worker
// deployment, through a durable queue.
//
// The /v1/audit handler that sees the connect event runs on an API pod. Doing the
// work there in a goroutine kept it off the worker pods it was meant for, and lost
// it without a retry whenever that pod restarted mid-run.
package agentconnect

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"nudgebee/services/common"
	"nudgebee/services/config"
	"nudgebee/services/recommendation"
	"nudgebee/services/security"
	"nudgebee/services/spend"
)

// The queue is internal to this package — published and consumed only here — so
// its names are constants rather than config.
const (
	exchangeName = "agent_connect_exchange"
	queueName    = "agent_connect"
)

// initialScansTimeout bounds the scan dispatch, matching the budget the API-pod
// path has always had. Together with the OpenCost sync's own 5-minute bound it
// keeps a message well inside the broker's consumer_timeout (3600s on our
// installs), past which the channel is closed and the message redelivered.
const initialScansTimeout = 15 * time.Minute

// Message is what the /v1/audit handler publishes for one connected agent.
type Message struct {
	AccountId string `json:"account_id"`
	TenantId  string `json:"tenant_id"`
}

// Indirection seams so processAgentConnected is unit-testable without a live DB.
var (
	newTenantAdminContext = security.NewSecurityContextForTenantAdmin
	triggerInitialScansFn = recommendation.TriggerInitialScans
	runServerSideOpenCost = spend.RunServerSideOnConnect
)

func init() {
	// Do not dial RabbitMQ during tests.
	if testing.Testing() {
		return
	}
	err := common.MqConsume(
		exchangeName,
		queueName,
		queueName,
		config.Config.RabbitMqAgentConnectConcurrency,
		processAgentConnected,
		common.MqConsumeWithDurableQueue(),
	)
	if err != nil {
		slog.Error("agent_connect_queue: failed to start consumer", "error", err)
	}
}

// Publish queues the first-connect work for one account.
//
// Durable and persistent so a broker restart doesn't drop it, and mandatory so a
// publish nobody can receive — the worker has not declared the queue yet, as on a
// fresh install — returns an error instead of a successful confirm. Callers must
// run the work themselves when this fails.
func Publish(ctx context.Context, accountId, tenantId string) error {
	return common.MqPublish(
		exchangeName,
		queueName,
		Message{AccountId: accountId, TenantId: tenantId},
		common.MqPublishWithPersistentDelivery(),
		common.MqPublishWithMandatory(),
		common.MqPublishWithContext(ctx),
	)
}

// processAgentConnected runs the first-connect work for one account.
//
// It returns nil on every path. MqConsume NackRequeues on an error, with no
// dead-letter queue and no delivery limit, so a returned error would redeliver
// forever. A failed run is retried the way it always has been: TriggerInitialScans
// writes its once-ever marker only after a successful dispatch, so the next
// connect runs it again. What the queue adds is that a worker killed mid-run
// leaves the message unacked, and the broker redelivers it.
func processAgentConnected(msgCtx context.Context, data []byte) error {
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		slog.Error("agent_connect_queue: failed to unmarshal message, dropping", "error", err, "data", string(data))
		return nil
	}
	accountId := strings.TrimSpace(msg.AccountId)
	tenantId := strings.TrimSpace(msg.TenantId)
	if accountId == "" || tenantId == "" {
		slog.Error("agent_connect_queue: message missing account_id or tenant_id, dropping",
			"account_id", accountId, "tenant_id", tenantId)
		return nil
	}
	secCtx := newTenantAdminContext(tenantId)
	if secCtx == nil {
		slog.Error("agent_connect_queue: nil tenant-admin context, dropping", "account_id", accountId, "tenant_id", tenantId)
		return nil
	}
	logger := slog.Default().With("account_id", accountId, "tenant_id", tenantId)

	// Scans before OpenCost, deliberately. TriggerInitialScans writes its marker as
	// soon as dispatch finishes, so if the worker is OOM-killed afterwards — the
	// image scan it has just started is the likely cause — the redelivered message
	// finds the marker and does not start a second batch of Jobs on the cluster.
	func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("agent_connect_queue: panic triggering initial scans", "panic", r)
			}
		}()
		scanCtx, cancel := context.WithTimeout(msgCtx, initialScansTimeout)
		defer cancel()
		if err := triggerInitialScansFn(security.NewRequestContext(scanCtx, secCtx, logger, nil, nil), accountId, tenantId); err != nil {
			logger.Error("agent_connect_queue: error triggering initial scans", "error", err)
		}
	}()

	runServerSideOpenCost(security.NewRequestContext(msgCtx, secCtx, logger, nil, nil), accountId, tenantId)
	return nil
}
