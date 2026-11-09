package core

import (
	"fmt"
	"strings"
	"time"
)

// LiveStatus is what the chat shows while a model call waits on a retry, a
// fallback or a summary. It is stored per agent under the running message's
// metadata.live_status, so parallel sub-agents never overwrite each other.
type LiveStatus struct {
	Kind        string     `json:"kind"`
	Text        string     `json:"text"`
	RetryAt     *time.Time `json:"retry_at,omitempty"`
	Attempt     int        `json:"attempt,omitempty"`
	MaxAttempts int        `json:"max_attempts,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

const (
	liveStatusRetrying    = "retrying"
	liveStatusFallback    = "fallback"
	liveStatusSummarising = "summarising"
)

// liveStatusKey identifies this call's entry; sub-agents each have their own id.
func (rc *retryContext) liveStatusKey() string {
	if rc.agentId != "" {
		return rc.agentId
	}
	return rc.agentName
}

// liveSubject names the model the way the final failure message would: the
// customer's config by name, our environment model never.
func (rc *retryContext) liveSubject() string {
	if !isCustomerLLMConfig(rc.resolution) {
		return "The platform AI model"
	}
	if !rc.liveConfigNameLoaded {
		rc.liveConfigName = llmConfigName(rc.ctx, rc.accountId, rc.resolution)
		rc.liveConfigNameLoaded = true
	}
	f := &LLMFailure{CustomerConfig: true, ConfigName: rc.liveConfigName, Provider: rc.currentProvider, Model: rc.currentModel}
	return f.describeConfig()
}

// liveProblem is one sentence saying what went wrong with the last attempt.
func (rc *retryContext) liveProblem() string {
	who := rc.liveSubject()
	if rc.lastErr == nil {
		// The circuit breaker skipped the model without calling it.
		return who + " is temporarily unavailable."
	}
	switch classifyLLMFailure(rc.lastErr) {
	case LLMFailureRateLimited:
		return who + " is rate limited."
	case LLMFailureUnavailable:
		return "Couldn't reach " + lowerFirst(who) + "."
	case LLMFailureGarbledReply:
		return who + " sent a reply that couldn't be read."
	case LLMFailureOutOfCredits:
		return who + " is out of credits."
	case LLMFailureInvalidKey:
		return who + " rejected its API key."
	case LLMFailureMissingCredential:
		return who + " has no valid credential."
	case LLMFailureUnknownModel:
		return who + " uses a model name the provider doesn't recognise."
	case LLMFailureDailyCap:
		return who + " has reached its daily limit."
	}
	if isEmptyResponseError(rc.lastErr) {
		return who + " returned an empty reply."
	}
	return who + " returned an error."
}

func (rc *retryContext) setLiveRetry(wait time.Duration, attempt, maxAttempts int) {
	if rc == nil {
		return
	}
	retryAt := time.Now().UTC().Add(wait)
	rc.setLiveStatus(LiveStatus{Kind: liveStatusRetrying, Text: rc.liveProblem(), RetryAt: &retryAt, Attempt: attempt, MaxAttempts: maxAttempts})
}

func (rc *retryContext) setLiveFallback(next FallbackModel) {
	if rc == nil {
		return
	}
	target := "a fallback model"
	if isCustomerLLMConfig(rc.resolution) && next.Model != "" {
		target = "fallback model " + next.Model
	}
	rc.setLiveStatus(LiveStatus{Kind: liveStatusFallback, Text: fmt.Sprintf("%s Trying %s.", rc.liveProblem(), target)})
}

func (rc *retryContext) setLiveSummarising() {
	if rc == nil {
		return
	}
	text := fmt.Sprintf("Context too long for %s. Summarising earlier steps…", lowerFirst(rc.liveSubject()))
	rc.setLiveStatus(LiveStatus{Kind: liveStatusSummarising, Text: text})
}

func (rc *retryContext) setLiveStatus(status LiveStatus) {
	if rc == nil || rc.messageId == "" || rc.liveStatusKey() == "" {
		return
	}
	status.Text = strings.TrimSpace(status.Text)
	status.UpdatedAt = time.Now().UTC()
	if err := GetConversationDao().SetMessageLiveStatus(rc.messageId, rc.liveStatusKey(), &status); err != nil {
		rc.ctx.GetLogger().Debug("live status: unable to save", "error", err, "message_id", rc.messageId)
		return
	}
	rc.liveStatusSet = true
}

// clearLiveStatus removes this call's entry, and only touches the database when
// an entry was written, so calls that never retried cost nothing.
func (rc *retryContext) clearLiveStatus() {
	if rc == nil || !rc.liveStatusSet {
		return
	}
	if err := GetConversationDao().SetMessageLiveStatus(rc.messageId, rc.liveStatusKey(), nil); err != nil {
		rc.ctx.GetLogger().Debug("live status: unable to clear", "error", err, "message_id", rc.messageId)
		return
	}
	rc.liveStatusSet = false
}
