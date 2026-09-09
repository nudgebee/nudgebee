package proxy

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"nudgebee/llm-gateway/auth"
	"nudgebee/llm-gateway/routing"
)

// recStage records that it ran and returns a fixed (stop, err).
type recStage struct {
	name string
	log  *[]string
	stop bool
	err  error
}

func (s recStage) Name() string { return s.name }
func (s recStage) Handle(*RequestContext) (bool, error) {
	*s.log = append(*s.log, s.name)
	return s.stop, s.err
}

func TestPipeline_RunsInOrderUntilStop(t *testing.T) {
	var log []string
	p := NewPipeline(
		recStage{name: "a", log: &log},
		recStage{name: "b", log: &log, stop: true}, // short-circuits
		recStage{name: "c", log: &log},
	)
	stop, err := p.Run(&RequestContext{})
	require.NoError(t, err)
	assert.True(t, stop)
	assert.Equal(t, []string{"a", "b"}, log, "c must not run after b stops")
}

func TestPipeline_HaltsOnError(t *testing.T) {
	var log []string
	boom := errors.New("boom")
	p := NewPipeline(
		recStage{name: "a", log: &log},
		recStage{name: "b", log: &log, err: boom},
		recStage{name: "c", log: &log},
	)
	stop, err := p.Run(&RequestContext{})
	assert.False(t, stop)
	assert.ErrorIs(t, err, boom)
	assert.Equal(t, []string{"a", "b"}, log)
}

// fakeCreds is a CredResolver stub.
type fakeCreds struct {
	key      schemas.Key
	ok       bool
	provider *schemas.ModelProvider
}

func (f fakeCreds) Resolve(_ context.Context, provider schemas.ModelProvider, _ auth.Identity) (schemas.Key, bool) {
	if f.provider != nil {
		*f.provider = provider
	}
	return f.key, f.ok
}

func TestResolverStage_InjectsDirectKeyWhenResolved(t *testing.T) {
	bctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	defer cancel()
	rc := &RequestContext{Bctx: bctx, Provider: schemas.Anthropic, Identity: auth.Identity{TenantID: "t1"}}

	st := resolverStage{creds: fakeCreds{key: schemas.Key{ID: "tenant-key"}, ok: true}}
	stop, err := st.Handle(rc)
	require.NoError(t, err)
	assert.False(t, stop)

	got, ok := bctx.Value(schemas.BifrostContextKeyDirectKey).(schemas.Key)
	require.True(t, ok, "a resolved tenant key must be set as the direct key")
	assert.Equal(t, "tenant-key", got.ID)
}

func TestResolverStage_FallsThroughWhenNoTenantCred(t *testing.T) {
	bctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	defer cancel()
	rc := &RequestContext{Bctx: bctx, Provider: schemas.Anthropic}

	st := resolverStage{creds: noopCredResolver{}}
	stop, err := st.Handle(rc)
	require.NoError(t, err)
	assert.False(t, stop)
	assert.Nil(t, bctx.Value(schemas.BifrostContextKeyDirectKey),
		"no tenant cred → no direct key → falls back to the operator/account default")
}

func TestResolverStage_SubstitutionUsesTargetCredentialNotMappedSource(t *testing.T) {
	bctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	defer cancel()
	var resolvedProvider schemas.ModelProvider
	rc := &RequestContext{
		Bctx:      bctx,
		Provider:  schemas.Vertex,
		DirectKey: &schemas.Key{ID: "mapped-vertex-account"},
		Decision: routing.Decision{
			Reason:           routing.ReasonSubstitute,
			ResolvedProvider: string(schemas.OpenAI),
		},
	}

	st := resolverStage{creds: fakeCreds{
		key: schemas.Key{ID: "target-openai-account"}, ok: true, provider: &resolvedProvider,
	}}
	stop, err := st.Handle(rc)
	require.NoError(t, err)
	assert.False(t, stop)
	assert.Equal(t, schemas.OpenAI, resolvedProvider)
	got, ok := bctx.Value(schemas.BifrostContextKeyDirectKey).(schemas.Key)
	require.True(t, ok)
	assert.Equal(t, "target-openai-account", got.ID)
}

func TestResolverStage_SubstitutionUsesTargetModelMapping(t *testing.T) {
	bctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	defer cancel()
	previous := modelResolverHook
	modelResolverHook = func(tenantID, model string) (schemas.ModelProvider, schemas.Key, string, string, bool) {
		assert.Equal(t, "t1", tenantID)
		assert.Equal(t, "target-alias", model)
		return schemas.VLLM, schemas.Key{ID: "target-mapped-account"}, "served-target", "/custom/path", true
	}
	t.Cleanup(func() { modelResolverHook = previous })
	rc := &RequestContext{
		Bctx: bctx, Provider: schemas.Vertex, MappedModel: "stale-source-served",
		Identity: auth.Identity{TenantID: "t1"},
		Decision: routing.Decision{Reason: routing.ReasonSubstitute, ResolvedProvider: string(schemas.VLLM), ResolvedModel: "target-alias"},
	}

	stop, err := (resolverStage{creds: noopCredResolver{}}).Handle(rc)
	require.NoError(t, err)
	assert.False(t, stop)
	assert.Equal(t, "served-target", rc.MappedModel)
	got, ok := bctx.Value(schemas.BifrostContextKeyDirectKey).(schemas.Key)
	require.True(t, ok)
	assert.Equal(t, "target-mapped-account", got.ID)
	assert.Equal(t, "/custom/path", bctx.Value(schemas.BifrostContextKeyURLPath))
}

func TestResolverStage_SubstitutionClearsSourceMapping(t *testing.T) {
	bctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	defer cancel()
	previous := modelResolverHook
	modelResolverHook = func(string, string) (schemas.ModelProvider, schemas.Key, string, string, bool) {
		return "", schemas.Key{}, "", "", false
	}
	t.Cleanup(func() { modelResolverHook = previous })
	rc := &RequestContext{
		Bctx: bctx, Provider: schemas.Vertex, MappedModel: "stale-source-served",
		Decision: routing.Decision{Reason: routing.ReasonSubstitute, ResolvedProvider: string(schemas.OpenAI), ResolvedModel: "gpt-5"},
	}

	stop, err := (resolverStage{creds: fakeCreds{key: schemas.Key{ID: "openai"}, ok: true}}).Handle(rc)
	require.NoError(t, err)
	assert.False(t, stop)
	assert.Empty(t, rc.MappedModel)
}

func TestResolverStage_SubstitutionChecksTargetOperatorCredential(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bctx, cancel := schemas.NewBifrostContextWithCancel(context.Background())
	defer cancel()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var checked schemas.ModelProvider
	prev := operatorCredsHook
	operatorCredsHook = func(provider schemas.ModelProvider) bool {
		checked = provider
		return false
	}
	t.Cleanup(func() { operatorCredsHook = prev })
	rc := &RequestContext{
		Gin: c, Bctx: bctx, Provider: schemas.Vertex,
		Decision: routing.Decision{Reason: routing.ReasonSubstitute, ResolvedProvider: string(schemas.OpenAI)},
	}

	stop, err := (resolverStage{creds: noopCredResolver{}}).Handle(rc)
	require.NoError(t, err)
	assert.True(t, stop)
	assert.Equal(t, schemas.OpenAI, checked)
	assert.Contains(t, rec.Body.String(), "No openai credential")
}

// fakeRouter resolves every request to a fixed model.
type fakeRouter struct{ resolved string }

func (f fakeRouter) Resolve(in routing.Input) routing.Decision {
	return routing.Decision{RequestedModel: in.Model, ResolvedModel: f.resolved}
}

func TestRouteStage_CapturesDecisionAndRewritesModel(t *testing.T) {
	rc := &RequestContext{
		Provider: schemas.Anthropic,
		Model:    "requested-model",
		Path:     "/v1/messages",
		Body:     []byte(`{"model":"requested-model","max_tokens":8}`),
	}
	st := routeStage{router: fakeRouter{resolved: "resolved-model"}}
	stop, err := st.Handle(rc)
	require.NoError(t, err)
	assert.False(t, stop)

	assert.Equal(t, "resolved-model", rc.Model, "model rewritten to the resolved target")
	assert.Equal(t, "requested-model", rc.Decision.RequestedModel)
	assert.Equal(t, "resolved-model", rc.Decision.ResolvedModel)
	assert.Contains(t, string(rc.Body), "resolved-model", "body model field rewritten")
}
