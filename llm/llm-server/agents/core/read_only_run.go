package core

import (
	"context"
	"fmt"
	"strings"

	"nudgebee/llm/common"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// readOnlyRunKey marks a run that no person is watching, such as the automatic
// investigation of an alert. Such a run cannot get an approval answered, so a
// write it decides to make is refused on the spot instead of waiting on a
// prompt nobody sees. The mark lives on the context of this run; the run's
// message also saves it (NBQueryConfig.ReadOnlyRun) so a recovered run stays
// read-only. A person's later turn on the same conversation does not inherit
// it and keeps its approval prompts.
type readOnlyRunKey struct{}

// WithReadOnlyRun returns a copy of ctx for a run that runs read-only: every
// action classified as a write is refused as a failed step. Use it for runs
// with no person to approve changes.
func WithReadOnlyRun(ctx *security.RequestContext) *security.RequestContext {
	if ctx == nil {
		return nil
	}
	return ctx.WithContext(context.WithValue(ctx.GetContext(), readOnlyRunKey{}, true))
}

// IsReadOnlyRun reports whether ctx belongs to a read-only run.
func IsReadOnlyRun(ctx *security.RequestContext) bool {
	if ctx == nil {
		return false
	}
	readOnly, _ := ctx.GetContext().Value(readOnlyRunKey{}).(bool)
	return readOnly
}

// readOnlyRefusal is the observation for a write refused in a read-only run.
// The first refusal of a tool says how to get the evidence without a change;
// later ones say to stop trying writes. Both ask the model to report what it
// could not check, so the answer and the remediation stage still carry it.
func readOnlyRefusal(requestType toolcore.ToolRequestType, refusals int) string {
	if refusals > 1 {
		return fmt.Sprintf("Not run: this is a read-only investigation, and %d write actions have already been refused in it. Do not retry write actions. "+
			"Continue with read-only evidence, and list what you could not check under \"Not checked (needs write access)\" in your answer.", refusals-1)
	}
	return fmt.Sprintf("Not run: this is an automatic, read-only investigation (no person is available to approve changes), so this %s action was refused. "+
		"Get the same evidence a read-only way, for example: `redis-cli INFO` or `GET` instead of `SET`; "+
		"`kubectl exec <existing pod> -- curl -s <url>` instead of starting a debug pod; "+
		"the logs and metrics tools instead of commands on the host. "+
		"If there is no read-only way, do not retry: list it under \"Not checked (needs write access)\" in your answer with what it would have shown, and continue.", requestType)
}

// ReadOnlyChoiceRefusal is what a read-only run gets instead of asking a person
// to choose (which configuration, account or repository to use). Nobody answers
// in such a run, so the question would only stop it until the waiting expiry.
// It sends the model to name the choice itself when the alert or evidence
// settles it, and otherwise to carry on without it and report the gap, the way
// readOnlyRefusal does for writes.
func ReadOnlyChoiceRefusal(what string, options []string) string {
	choices := ""
	if len(options) > 0 {
		choices = " (" + strings.Join(options, ", ") + ")"
	}
	return fmt.Sprintf("Not asked: this is an automatic investigation with no one available to answer questions, so I can't ask which %s to use%s. "+
		"If the alert or the evidence so far identifies one, call again naming it explicitly. "+
		"Otherwise continue without it, and list it under \"Not checked\" in your answer with what it would have shown.", what, choices)
}

// ReadOnlyQuestionRefusal is what a read-only run gets instead of asking a
// person an open question: nobody is there to answer it.
const ReadOnlyQuestionRefusal = "Not asked: this is an automatic investigation with no one available to answer questions. " +
	"Continue with what the evidence shows, and state any assumption or open question in your answer."

// messageRanReadOnly reports whether a stored message was saved by a
// read-only run (see NBQueryConfig.ReadOnlyRun). Only the run's own messages
// carry it: a person's follow-up on the same conversation is a new message
// that does not inherit it.
func messageRanReadOnly(message ConversationMessage) bool {
	if message.MessageConfig == nil || *message.MessageConfig == "" {
		return false
	}
	var cfg toolcore.NBQueryConfig
	if err := common.UnmarshalJson([]byte(*message.MessageConfig), &cfg); err != nil {
		return false
	}
	return cfg.ReadOnlyRun
}
