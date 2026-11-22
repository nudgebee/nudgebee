package core

import (
	"errors"
)

var errAgentNotFound = errors.New("error: agent not found")

var ErrConversationInProgress = errors.New("conversation: conversation is already in progress")

// ErrConversationPendingFollowup is returned when a new (non-followup) generation
// arrives on a conversation whose latest turn is still WAITING / WAITING_FOR_CLIENT_TOOL
// on a followup answer. Without this gate, the new turn would orphan the prior
// turn at WAITING permanently.
var ErrConversationPendingFollowup = errors.New("conversation: previous turn is waiting on a followup answer; answer or cancel it before starting a new turn")

// ErrCleanupRefusedActiveFollowup is returned by CleanupConversationMessage when
// a non-terminal followup message still references an agent row in the deletion
// set. The right caller behavior is to flip the human message to WAITING and
// stop re-execution — proceeding would create duplicate agent/tool rows
// alongside the preserved-but-in-flight ones.
var ErrCleanupRefusedActiveFollowup = errors.New("conversation: cleanup refused; active followup references agents from this message")
