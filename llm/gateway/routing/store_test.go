package routing

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func rule(id, model, target string) Rule {
	return Rule{ID: id, Enabled: true, Priority: 100,
		Match:  Match{Provider: "anthropic", Model: model},
		Target: Target{Endpoint: Endpoint{Model: target}}}
}

func TestStore_StaticOnly(t *testing.T) {
	s := NewStore([]Rule{rule("cfg", "m", "from-config")}, nil, 0)
	d := s.Resolve(Input{Provider: "anthropic", Model: "m"})
	assert.Equal(t, "from-config", d.ResolvedModel)
	assert.Equal(t, "cfg", d.RuleID)
}

func TestStore_DBWinsTieOverConfig(t *testing.T) {
	static := []Rule{rule("cfg", "m", "from-config")}
	loader := func() ([]Rule, error) { return []Rule{rule("db", "m", "from-db")}, nil }
	s := NewStore(static, loader, time.Hour)
	defer func() { _ = s.Close() }()

	d := s.Resolve(Input{Provider: "anthropic", Model: "m"})
	assert.Equal(t, "from-db", d.ResolvedModel, "DB rule wins the tie over config")
	assert.Equal(t, "db", d.RuleID)
}

func TestStore_LoaderErrorFallsBackToStatic(t *testing.T) {
	static := []Rule{rule("cfg", "m", "from-config")}
	loader := func() ([]Rule, error) { return nil, errors.New("db down") }
	s := NewStore(static, loader, time.Hour)
	defer func() { _ = s.Close() }()

	d := s.Resolve(Input{Provider: "anthropic", Model: "m"})
	assert.Equal(t, "from-config", d.ResolvedModel, "DB error → serve static rules")
}

func TestStore_RefreshPicksUpNewRules(t *testing.T) {
	target := "v1"
	loader := func() ([]Rule, error) { return []Rule{rule("db", "m", target)}, nil }
	s := NewStore(nil, loader, time.Hour)
	defer func() { _ = s.Close() }()

	assert.Equal(t, "v1", s.Resolve(Input{Provider: "anthropic", Model: "m"}).ResolvedModel)
	target = "v2"
	s.rebuild() // simulate the refresh tick
	assert.Equal(t, "v2", s.Resolve(Input{Provider: "anthropic", Model: "m"}).ResolvedModel)
}

func TestStore_CloseDoesNotHang(t *testing.T) {
	s := NewStore(nil, func() ([]Rule, error) { return nil, nil }, time.Hour)
	done := make(chan struct{})
	go func() { _ = s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung")
	}
}
