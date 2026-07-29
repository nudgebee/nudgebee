package proxy

import (
	"context"
	"errors"
	"testing"

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
	key schemas.Key
	ok  bool
}

func (f fakeCreds) Resolve(context.Context, schemas.ModelProvider, auth.Identity) (schemas.Key, bool) {
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
