package api

import (
	"errors"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// errRunClaimNeedsServiceToken refuses a run claim on a server that does not
// check the service token: the claim headers are trusted only as far as that
// token, so without it nobody may make one.
var errRunClaimNeedsServiceToken = errors.New("api: a run claim needs the service token (LLM_SERVER_TOKEN), which this server does not check")

// runClaimOf reads a run claim from the request's X-NB-Run-* headers, and
// whether the request carries any of them.
func runClaimOf(c *gin.Context) (core.RunClaim, bool) {
	claim := core.RunClaim{
		Extension: strings.TrimSpace(c.GetHeader(core.RunClaimHeaderExtension)),
		Ref:       strings.TrimSpace(c.GetHeader(core.RunClaimHeaderRef)),
		Agent:     strings.TrimSpace(c.GetHeader(core.RunClaimHeaderAgent)),
	}
	return claim, claim.Extension != "" || claim.Ref != "" || claim.Agent != ""
}

// chatRunProfile is the governed-run profile of a chat request and, when the
// request creates a governed run, the profile to write on the new
// conversation.
//
//   - A conversation the request continues keeps its stored profile (nil for
//     an ordinary one); a claim on it is ignored.
//   - A request that creates a conversation becomes a governed run only
//     through a run claim, which the claimed extension turns into the
//     profile. A claim is refused unless the server checks the service
//     token: the token middleware has already refused a wrong one, so here
//     only "no token configured" remains.
func chatRunProfile(c *gin.Context, ctx *security.RequestContext, existing *core.Conversation, accountId string) (profile, created *core.RunProfile, err error) {
	if existing != nil && existing.ID != uuid.Nil {
		return existing.RunProfile, nil, nil
	}
	claim, claimed := runClaimOf(c)
	if !claimed {
		return nil, nil, nil
	}
	if config.Config.LlmServerToken == "" {
		return nil, nil, errRunClaimNeedsServiceToken
	}
	prepared, err := core.PrepareRun(ctx, claim, accountId)
	if err != nil {
		return nil, nil, err
	}
	return &prepared, &prepared, nil
}

// errRunAgentNotFound refuses a governed run whose pinned agent is gone.
var errRunAgentNotFound = errors.New("api: the governed run's agent was not found")

// pinRunAgent is the agent a turn runs with, given the agent chosen for it so
// far (current: the last agent reused from history, nil before routing). A
// governed run is pinned to its profile's agent instead, so nothing in the
// turn's text (an @mention included) or its history chooses the agent, and
// routing never runs for it; any other turn keeps current.
func pinRunAgent(ctx *security.RequestContext, profile *core.RunProfile, accountId string, current core.NBAgent) (core.NBAgent, error) {
	if profile == nil {
		return current, nil
	}
	agent, found := getRunAgent(ctx, profile.Agent, accountId, core.AgentStatusEnabled)
	if !found {
		return nil, errRunAgentNotFound
	}
	return agent, nil
}

// getRunAgent resolves a governed run's agent; a variable for tests.
var getRunAgent = core.GetNBAgent
