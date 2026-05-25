package mq

import (
	"context"
	"sync"
	"time"

	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"
)

// ConnectionManager manages a long-lived AMQP connection with auto-reconnect.
type ConnectionManager struct {
	url         string
	connMu      sync.RWMutex
	conn        *amqp.Connection
	notifyClose chan *amqp.Error
}

func NewConnectionManager(url string) (*ConnectionManager, error) {
	cm := &ConnectionManager{url: url}
	if err := cm.connect(); err != nil {
		slog.Warn("Initial RabbitMQ connection failed; will retry in background", "url", url, "error", err)
	}
	go cm.handleReconnect()
	return cm, nil
}

func (cm *ConnectionManager) connect() error {
	conn, err := amqp.Dial(cm.url)
	if err != nil {
		return err
	}

	cm.connMu.Lock()
	cm.conn = conn
	cm.notifyClose = conn.NotifyClose(make(chan *amqp.Error, 1))
	cm.connMu.Unlock()

	slog.Info("AMQP connected")
	return nil
}

func (cm *ConnectionManager) handleReconnect() {
	for {
		cm.connMu.RLock()
		conn := cm.conn
		notifyCh := cm.notifyClose
		cm.connMu.RUnlock()

		// If we have an active connection, wait for it to close.
		// If conn is nil (initial connection failed), we skip this and go straight to reconnect.
		if conn != nil && notifyCh != nil {
			if err := <-notifyCh; err != nil {
				slog.Warn("AMQP connection closed; reconnecting...", "err", err)
			} else {
				slog.Warn("AMQP connection closed; reconnecting...")
			}
		}

		// Reconnect loop: keep trying until successful.
		for {
			if err := cm.connect(); err != nil {
				slog.Error("RabbitMQ reconnection failed; will retry", "err", err)
				time.Sleep(2 * time.Second)
				continue
			}
			break
		}
	}
}

func (cm *ConnectionManager) GetChannel(ctx context.Context) (*amqp.Channel, error) {
	for {
		cm.connMu.RLock()
		conn := cm.conn
		cm.connMu.RUnlock()

		if conn == nil {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(500 * time.Millisecond):
				continue
			}
		}

		ch, err := conn.Channel()
		if err == nil {
			return ch, nil
		}

		slog.Warn("Failed to open AMQP channel; retrying", "err", err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (cm *ConnectionManager) Close() {
	cm.connMu.Lock()
	if cm.conn != nil {
		cm.conn.Close() // nolint
	}
	cm.connMu.Unlock()
}
