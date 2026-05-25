package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/tidwall/gjson"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"nudgebee/relay-server/pkg/config"
	"nudgebee/relay-server/pkg/db"
	"nudgebee/relay-server/pkg/mq"
	"nudgebee/relay-server/pkg/server/metrics"
	"nudgebee/relay-server/pkg/signing"
	"nudgebee/relay-server/pkg/utils"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

// RegisterHandler sets up a /register WebSocket that forwards RPCs to RabbitMQ in parallel,
// and properly handles shutdown and channel-reconnect without hitting "channel not open" errors.
func RegisterHandler(
	store db.AgentStore,
	connMgr *mq.ConnectionManager,
	topo *mq.Topology,
	cfg *config.Config,
	exchange string,
	signer *signing.Signer,
	roottracer *trace.Tracer,
	rootmeter *metric.Meter,
	rootLogger *slog.Logger,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		// —— setup context & logger ——
		ctx, cancel := context.WithCancel(c.Request.Context())
		defer cancel()
		logger, _, _ := utils.BuildContextFromPayload(c, roottracer, rootmeter, rootLogger)

		// —— identify queue & instrumentation ——
		accountID := c.GetString("accountID")
		agentType := c.GetString("agentType")
		if agentType == "" {
			agentType = "k8s" // default for backward compatibility
		}

		// Proxy agents get a separate queue to avoid message conflicts
		queue := fmt.Sprintf("relay_requests_%s", accountID)
		if agentType != "k8s" {
			queue = fmt.Sprintf("relay_requests_%s_%s", accountID, agentType)
		}
		logger.Info("[Register] starting session", "account", accountID, "agent_type", agentType, "queue", queue)

		sessionStart := time.Now()
		if metrics.AsyncMetricsInstance != nil {
			metrics.AsyncMetricsInstance.IncWSSessions(metrics.AttrAccount(accountID))
			defer func() {
				metrics.AsyncMetricsInstance.DecWSSessions(metrics.AttrAccount(accountID))
				metrics.AsyncMetricsInstance.RecordWSSessionDuration(time.Since(sessionStart).Seconds(), metrics.AttrAccount(accountID))
			}()
		}

		// —— upgrade to WebSocket ——
		ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			c.JSON(http.StatusInternalServerError, utils.BuildError(500, "ws upgrade failed"))
			return
		}
		defer ws.Close() // nolint:errcheck

		const (
			writeWait = 10 * time.Second
			pongWait  = 40 * time.Second
		)
		ws.SetReadDeadline(time.Now().Add(pongWait)) // nolint:errcheck

		// wsMu serializes all writes (WriteMessage + WriteControl) to the
		// websocket connection.  gorilla/websocket requires that callers
		// ensure no concurrent calls to write methods.
		var wsMu sync.Mutex

		// 1) Handle client pings: reset deadline & reply with Pong
		ws.SetPingHandler(func(appData string) error {
			// reset our read deadline
			ws.SetReadDeadline(time.Now().Add(pongWait)) // nolint:errcheck
			// send back a Pong frame with the same payload
			wsMu.Lock()
			defer wsMu.Unlock()
			return ws.WriteControl(
				websocket.PongMessage,
				[]byte(appData),
				time.Now().Add(writeWait),
			)
		})

		// 2) (Optional) If you ever send pings to the client, keep this to reset on their Pong:
		ws.SetPongHandler(func(string) error {
			ws.SetReadDeadline(time.Now().Add(pongWait)) // nolint:errcheck
			return nil
		})
		ws.SetCloseHandler(func(code int, text string) error {
			logger.Info("ws close received", "code", code, "text", text)
			cancel()
			return nil
		})

		// —— initial greeting ——
		_, greet, err := ws.ReadMessage()
		if err != nil {
			logger.Error("greeting read failed", "err", err)
			return
		}
		logger.Info("greeting", "payload", string(greet))

		// Extract agent build info from the greeting and persist it so the UI can
		// show which forager build a customer is running. Non-fatal — legacy
		// agents may not send these fields.
		var greeting struct {
			AgentVersion    string `json:"agent_version"`
			AgentCommit     string `json:"agent_commit"`
			AgentBuildTime  string `json:"agent_build_time"`
			ProtocolVersion string `json:"protocol_version"`
			Version         string `json:"version"` // legacy single-field form
		}
		if err := json.Unmarshal(greet, &greeting); err != nil {
			logger.Warn("greeting parse failed, skipping version persistence", "err", err)
		} else {
			version := greeting.AgentVersion
			if version == "" {
				version = greeting.Version
			}
			if err := store.UpdateAgentVersion(ctx, accountID, agentType, version, greeting.AgentCommit, greeting.AgentBuildTime, greeting.ProtocolVersion); err != nil {
				logger.Error("failed to persist agent version from greeting", "err", err, "account", accountID, "agent_type", agentType)
			} else if version != "" || greeting.AgentCommit != "" {
				logger.Info("persisted agent version from greeting",
					"account", accountID,
					"agent_type", agentType,
					"version", version,
					"commit", greeting.AgentCommit,
					"protocol_version", greeting.ProtocolVersion,
				)
			}
		}

		// —— update relay connection status when agent connects successfully ——
		if err := store.UpdateRelayConnectionStatus(ctx, accountID, agentType, true, sessionStart); err != nil {
			logger.Error("failed to update relay connection status", "err", err, "account", accountID, "agent_type", agentType)
			// Continue processing even if status update fails
		} else {
			logger.Info("updated relay connection status to true", "account", accountID, "agent_type", agentType)
		}

		// —— ensure tenant topology ——
		if err := topo.EnsureTenantForAgentType(ctx, accountID, agentType); err != nil {
			logger.Error("ensure tenant failed", "err", err, "agent_type", agentType)
			return
		}

		// —— shared state for dispatching replies ——
		wsWriteCh := make(chan []byte)
		var respMap struct {
			sync.Mutex
			m map[string]chan []byte
		}
		respMap.m = make(map[string]chan []byte)

		eg, egCtx := errgroup.WithContext(ctx)

		// safeSend avoids panics if wsWriteCh is closed
		safeSend := func(msg []byte) {
			defer func() {
				if r := recover(); r != nil {
					logger.Warn("wsWriteCh closed, skipping send", "recover", r)
				}
			}()
			select {
			case wsWriteCh <- msg:
			case <-egCtx.Done():
			}
		}

		// —— writer: pump wsWriteCh → WebSocket client ——
		eg.Go(func() error {
			for {
				select {
				case <-egCtx.Done():
					return egCtx.Err()
				case msg, ok := <-wsWriteCh:
					if !ok {
						return nil
					}
					wsMu.Lock()
					ws.SetWriteDeadline(time.Now().Add(writeWait)) // nolint:errcheck
					err := ws.WriteMessage(websocket.TextMessage, msg)
					wsMu.Unlock()
					if err != nil {
						if ce, ok := err.(*websocket.CloseError); ok {
							logger.Info("ws write closed", "code", ce.Code)
							return nil
						}
						return fmt.Errorf("ws write error: %w", err)
					}
				}
			}
		})

		// —— pinger: send periodic pings to keep the connection alive through LBs ——
		eg.Go(func() error {
			const pingInterval = 30 * time.Second
			ticker := time.NewTicker(pingInterval)
			defer ticker.Stop()
			for {
				select {
				case <-egCtx.Done():
					return egCtx.Err()
				case <-ticker.C:
					wsMu.Lock()
					ws.SetWriteDeadline(time.Now().Add(writeWait)) // nolint:errcheck
					err := ws.WriteMessage(websocket.PingMessage, nil)
					wsMu.Unlock()
					if err != nil {
						return fmt.Errorf("ws ping error: %w", err)
					}
				}
			}
		})

		// —— reader: demultiplex replies from client, handle unsolicited agent messages ——
		eg.Go(func() error {
			for {
				ws.SetReadDeadline(time.Now().Add(pongWait)) // nolint:errcheck
				_, raw, err := ws.ReadMessage()
				if err != nil {
					if ce, ok := err.(*websocket.CloseError); ok {
						logger.Info("ws read closed", "code", ce.Code)
						return nil
					}
					return fmt.Errorf("ws read error: %w", err)
				}
				var msg struct {
					RequestID string `json:"request_id"`
					Action    string `json:"action"`
				}
				if err := json.Unmarshal(raw, &msg); err != nil {
					metrics.WS_MessageErrors.Add(ctx, 1, metric.WithAttributes(metrics.AttrAccount(accountID)))
					continue
				}

				// Handle unsolicited agent messages (no request_id, has action)
				if msg.Action == "datasource_health_update" {
					go handleDatasourceHealthUpdate(egCtx, store, accountID, agentType, raw, logger)
					continue
				}
				if msg.Action == "datasource_inventory" {
					go handleDatasourceInventory(egCtx, store, accountID, agentType, raw, logger)
					continue
				}
				if msg.Action == "datasource_metadata" {
					go handleDatasourceMetadata(egCtx, store, accountID, agentType, raw, logger)
					continue
				}

				respMap.Lock()
				if ch, ok := respMap.m[msg.RequestID]; ok {
					select {
					case ch <- raw:
					case <-egCtx.Done():
					}
				} else {
					logger.Warn("no handler for reply", "request_id", msg.RequestID)
				}
				respMap.Unlock()
			}
		})

		consumerTag := fmt.Sprintf("reg-%s-%d", accountID, time.Now().UnixNano())
		// —— consumer + dispatch with auto‐reconnect ——
		eg.Go(func() error {
			for {
				// 1) open a fresh channel
				sessCh, err := connMgr.GetChannel(egCtx)
				if err != nil {
					return fmt.Errorf("get channel: %w", err)
				}

				// 2) apply QoS
				if err := sessCh.Qos(10, 0, false); err != nil {
					sessCh.Close() // nolint:errcheck
					logger.Error("QoS setup failed, retrying", "err", err)
					time.Sleep(time.Second)
					continue
				}

				// 3) watch for channel-close
				closeErrCh := sessCh.NotifyClose(make(chan *amqp.Error, 1))

				// 4) start consuming
				msgs, err := sessCh.Consume(queue, consumerTag, false, false, false, false, nil)
				if err != nil {
					sessCh.Close() // nolint:errcheck
					logger.Error("Consume failed, retrying", "err", err)
					time.Sleep(time.Second)
					continue
				}

			ConsumeLoop:
				for {
					select {
					case <-egCtx.Done():
						// clean shutdown
						sessCh.Cancel(consumerTag, false) // nolint
						sessCh.Close()                    // nolint:errcheck
						return egCtx.Err()

					case amqpErr := <-closeErrCh:
						logger.Warn("AMQP channel closed; reconnecting", "err", amqpErr)
						sessCh.Close() // nolint:errcheck
						break ConsumeLoop

					case d, ok := <-msgs:
						if !ok {
							// broker closed the msgs channel—reconnect
							logger.Warn("msgs channel closed; reconnecting")
							sessCh.Close() // nolint:errcheck
							break ConsumeLoop
						}
						// dispatch each delivery in its own goroutine
						eg.Go(func() error {
							start := time.Now()
							defer func() {
								metrics.WS_InFlightRequests.Add(ctx, -1, metric.WithAttributes(metrics.AttrAccount(accountID)))
								metrics.WS_RequestDuration.Record(ctx, time.Since(start).Seconds(), metric.WithAttributes(metrics.AttrAccount(accountID)))
							}()
							if metrics.AsyncMetricsInstance != nil {
								metrics.AsyncMetricsInstance.IncWSInFlightRequests(metrics.AttrAccount(accountID))
								metrics.AsyncMetricsInstance.IncWSMessages(metrics.AttrAccount(accountID))
							}

							// Extract action details using zero-copy gjson
							actionName := gjson.GetBytes(d.Body, "body.action_name").String()
							requestID := gjson.GetBytes(d.Body, "request_id").String()

							deliveryLogger := logger.With(
								"corr_id", d.CorrelationId,
								"request_id", requestID,
								"action", actionName,
								"account", accountID,
							)

							deliveryLogger.Info("processing action")

							// forward to WebSocket
							safeSend(d.Body)

							// wait for reply or timeout
							ch := make(chan []byte, 1)
							respMap.Lock()
							respMap.m[d.CorrelationId] = ch
							deliveryLogger.Debug("awaiting reply")
							respMap.Unlock()

							// Defer cleanup of response map
							defer func() {
								respMap.Lock()
								delete(respMap.m, d.CorrelationId)
								respMap.Unlock()
							}()

							timeout := time.NewTimer(cfg.HTTP.ReadTimeout)
							defer timeout.Stop()

							select {
							case reply := <-ch:
								deliveryLogger.Info("received reply from agent")

								err := sessCh.Publish(
									"", d.ReplyTo, false, false,
									amqp.Publishing{
										ContentType:   "application/json",
										Body:          reply,
										CorrelationId: d.CorrelationId,
										Timestamp:     time.Now(),
									},
								)
								if err != nil {
									deliveryLogger.Error("publish failed in register", "err", err)
									d.Nack(false, false) // nolint
									metrics.WS_MessageErrors.Add(ctx, 1, metric.WithAttributes(metrics.AttrAccount(accountID)))
								} else {
									deliveryLogger.Info("reply published successfully")
									d.Ack(false) // nolint
								}
								logger.Debug("reply sent", "corr_id", d.CorrelationId, "account", accountID)
							case <-timeout.C:
								d.Nack(false, false) // nolint
								deliveryLogger.Warn("request timeout")
								metrics.WS_RequestTimeouts.Add(ctx, 1, metric.WithAttributes(metrics.AttrAccount(accountID)))
							case <-egCtx.Done():
								deliveryLogger.Info("context canceled, sending error reply")
								// Send an error reply matching AgentResponse format so the
								// RPC caller fails fast instead of waiting for the full timeout.
								errReply := []byte(`{"status_code":502,"action":"error","data":"agent connection lost"}`)
								if err := sessCh.Publish(
									"", d.ReplyTo, false, false,
									amqp.Publishing{
										ContentType:   "application/json",
										Body:          errReply,
										CorrelationId: d.CorrelationId,
										Timestamp:     time.Now(),
									},
								); err != nil {
									deliveryLogger.Warn("failed to publish error reply", "err", err)
									d.Nack(false, false) // nolint
								} else {
									d.Ack(false) // nolint
								}
							}

							return nil
						})
					}
				}
			}
		})

		// —— initial config sync for proxy agents ——
		if agentType == db.AgentTypeProxy {
			eg.Go(func() error {
				// Brief delay to let the consumer goroutine start consuming
				select {
				case <-time.After(2 * time.Second):
				case <-egCtx.Done():
					return egCtx.Err()
				}

				datasources, err := store.QueryProxyDatasources(egCtx, accountID)
				if err != nil {
					logger.Error("failed to query proxy datasources for initial sync", "account_id", accountID, "err", err)
					return nil
				}
				if len(datasources) == 0 {
					logger.Info("no proxy datasources to sync on connect", "account_id", accountID)
					return nil
				}

				reqID := uuid.NewString()
				pushMsg := map[string]any{
					"action":      "datasource_config_sync",
					"request_id":  reqID,
					"account_id":  accountID,
					"datasources": datasources,
				}
				payload, err := json.Marshal(pushMsg)
				if err != nil {
					logger.Error("failed to marshal initial config sync", "err", err)
					return nil
				}

				// Register in respMap to wait for ack
				ch := make(chan []byte, 1)
				respMap.Lock()
				respMap.m[reqID] = ch
				respMap.Unlock()
				defer func() {
					respMap.Lock()
					delete(respMap.m, reqID)
					respMap.Unlock()
				}()

				payload = signPayload(payload, signer, logger)
				safeSend(payload)
				logger.Info("sent initial config sync to proxy agent",
					"account_id", accountID,
					"datasource_count", len(datasources),
					"request_id", reqID,
				)

				select {
				case <-ch:
					logger.Info("proxy agent acknowledged initial config sync", "account_id", accountID, "request_id", reqID)
				case <-time.After(30 * time.Second):
					logger.Warn("proxy agent did not ack initial config sync within timeout", "account_id", accountID, "request_id", reqID)
				case <-egCtx.Done():
				}
				return nil
			})
		}

		// —— wait for everything to finish ——
		if err := eg.Wait(); err != nil && err != context.Canceled {
			logger.Error("register session ended with error", "err", err, "account", accountID)
		} else {
			logger.Info("register session ended cleanly", "account", accountID)
		}

		// —— mark agent as disconnected (only if no newer session has connected) ——
		if derr := store.UpdateRelayConnectionStatus(context.Background(), accountID, agentType, false, sessionStart); derr != nil {
			logger.Error("failed to update relay disconnect status", "err", derr, "account", accountID, "agent_type", agentType)
		} else {
			logger.Info("updated relay connection status to false", "account", accountID, "agent_type", agentType)
		}
	}
}

// handleDatasourceHealthUpdate processes health status messages from proxy agents
// and persists them to the agent's connection_status JSONB in the database.
//
// Note: the forager sends agent_version/agent_commit with every health tick, but
// we deliberately don't persist them here. The forager's version is linker-injected
// and immutable within a process — any real upgrade path (installer, Helm rolling
// update, pod restart, network flap) opens a new websocket session, which re-fires
// the greeting and re-captures the version. Writing the same value every tick would
// just add churn to a hot row.
func handleDatasourceHealthUpdate(ctx context.Context, store db.AgentStore, accountID, agentType string, raw []byte, logger *slog.Logger) {
	var msg struct {
		Action      string         `json:"action"`
		Datasources map[string]any `json:"datasources"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		logger.Error("failed to parse datasource health update", "err", err)
		return
	}

	if len(msg.Datasources) == 0 {
		return
	}

	if err := store.UpdateDatasourceHealth(ctx, accountID, agentType, msg.Datasources); err != nil {
		logger.Error("failed to persist datasource health", "account_id", accountID, "err", err)
		return
	}

	logger.Info("datasource health updated", "account_id", accountID, "datasource_count", len(msg.Datasources))
}

// handleDatasourceInventory processes inventory messages from proxy agents
// and auto-registers locally configured datasources as integrations.
func handleDatasourceInventory(ctx context.Context, store db.AgentStore, accountID, agentType string, raw []byte, logger *slog.Logger) {
	var msg struct {
		Action      string               `json:"action"`
		Datasources []db.AgentDatasource `json:"datasources"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		logger.Error("failed to parse datasource inventory", "err", err)
		return
	}

	if len(msg.Datasources) == 0 {
		return
	}

	if err := store.UpsertAgentDatasources(ctx, accountID, agentType, msg.Datasources); err != nil {
		logger.Error("failed to upsert agent datasources", "account_id", accountID, "err", err)
		return
	}

	logger.Info("agent datasources registered", "account_id", accountID, "datasource_count", len(msg.Datasources))
}

// handleDatasourceMetadata processes metadata messages from proxy agents
// and persists version/connection info to the integrations table.
func handleDatasourceMetadata(ctx context.Context, store db.AgentStore, accountID, agentType string, raw []byte, logger *slog.Logger) {
	var msg struct {
		Action   string                    `json:"action"`
		Metadata map[string]map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		logger.Error("failed to parse datasource metadata", "err", err)
		return
	}

	if len(msg.Metadata) == 0 {
		return
	}

	if err := store.UpdateDatasourceMetadata(ctx, accountID, agentType, msg.Metadata); err != nil {
		logger.Error("failed to persist datasource metadata", "account_id", accountID, "err", err)
		return
	}

	logger.Info("datasource metadata updated", "account_id", accountID, "datasource_count", len(msg.Metadata))
}
