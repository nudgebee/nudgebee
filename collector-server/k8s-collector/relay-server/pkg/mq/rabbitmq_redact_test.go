package mq

import (
	"strings"
	"testing"
)

func TestRedactAMQPURL(t *testing.T) {
	got := redactAMQPURL("amqp://nudgebee:s3cret@rabbitmq:5672/vhost")
	if strings.Contains(got, "s3cret") {
		t.Fatalf("password leaked: %q", got)
	}
	if !strings.Contains(got, "rabbitmq:5672") {
		t.Fatalf("host lost: %q", got)
	}
	if got := redactAMQPURL("amqp://rabbitmq:5672/"); got != "amqp://rabbitmq:5672/" {
		t.Fatalf("credential-free URL changed: %q", got)
	}
}
