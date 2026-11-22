package core

import (
	"errors"
	"testing"

	"nudgebee/services/security"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsForagerRegisteredDatasource(t *testing.T) {
	t.Run("a user row and the agent's own row are not, and need no query", func(t *testing.T) {
		for _, dto := range []IntegrationDto{
			{Id: "u", Source: "user", Name: "mine", Type: "prometheus"},
			{Id: "a", Source: "agent", Name: "prometheus", Type: "prometheus"},
		} {
			got, err := IsForagerRegisteredDatasource(dto)
			require.NoError(t, err)
			assert.False(t, got, dto.Id)
		}
		assert.NoError(t, pkgMock.ExpectationsWereMet(), "no query must have been expected or run")
	})

	t.Run("an agent row under its own name is one when both config rows exist", func(t *testing.T) {
		pkgMock.ExpectQuery(`SELECT EXISTS`).WithArgs("fp").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		got, err := IsForagerRegisteredDatasource(IntegrationDto{Id: "fp", Source: "agent", Name: "vm-fleet-prom", Type: "prometheus"})
		require.NoError(t, err)
		assert.True(t, got)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("an agent row under its own name without the config rows is not", func(t *testing.T) {
		pkgMock.ExpectQuery(`SELECT EXISTS`).WithArgs("k8s-other").
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
		got, err := IsForagerRegisteredDatasource(IntegrationDto{Id: "k8s-other", Source: "agent", Name: "prometheus-k8s", Type: "prometheus"})
		require.NoError(t, err)
		assert.False(t, got, "a name that differs from the type is not enough")
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})

	t.Run("a row listed with its config is answered without a query", func(t *testing.T) {
		cfg := func(kv ...string) []IntegrationConfigValue {
			var out []IntegrationConfigValue
			for i := 0; i < len(kv); i += 2 {
				out = append(out, IntegrationConfigValue{Name: kv[i], Value: kv[i+1]})
			}
			return out
		}
		base := IntegrationDto{Id: "fp", Source: "agent", Name: "vm-fleet-prom", Type: "prometheus"}

		base.Configs = cfg("connection_mode", "vm_agent", "datasource_key", "local:vm-fleet-prom")
		got, err := IsForagerRegisteredDatasource(base)
		require.NoError(t, err)
		assert.True(t, got)

		base.Configs = cfg("connection_mode", "vm_agent") // no key
		got, _ = IsForagerRegisteredDatasource(base)
		assert.False(t, got)

		base.Configs = cfg("datasource_key", "k", "connection_mode", "direct")
		got, _ = IsForagerRegisteredDatasource(base)
		assert.False(t, got)
		assert.NoError(t, pkgMock.ExpectationsWereMet(), "no query must have been run")
	})

	t.Run("a failed read is an error, not a no", func(t *testing.T) {
		pkgMock.ExpectQuery(`SELECT EXISTS`).WithArgs("x").WillReturnError(errors.New("connection reset"))
		_, err := IsForagerRegisteredDatasource(IntegrationDto{Id: "x", Source: "agent", Name: "n", Type: "loki"})
		require.Error(t, err)
		assert.NoError(t, pkgMock.ExpectationsWereMet())
	})
}

// activeOf is the activeIDs lookup of an account whose listed rows are all enabled.
func activeOf(ids ...string) func() (map[string]bool, error) {
	return func() (map[string]bool, error) {
		out := map[string]bool{}
		for _, id := range ids {
			out[id] = true
		}
		return out, nil
	}
}

func noDefault() (string, error) { return "", nil }

// ListIntegrationConfigs returns disabled rows. Without the active filter a disabled
// hand-added row, which sorts first, was chosen over the enabled forager-registered one
// whenever the account had no default for the signal.
func TestPickServerReachable_ActiveRows(t *testing.T) {
	user := IntegrationDto{Id: "user", Source: "user", Type: "prometheus"}
	forager := IntegrationDto{Id: "forager", Source: "agent", Name: "vm-fleet-prom", Type: "prometheus"}
	registered := func(d IntegrationDto) (bool, error) { return d.Id == "forager", nil }

	t.Run("a disabled hand-added row loses to an enabled forager-registered one", func(t *testing.T) {
		got, found, err := pickServerReachable([]IntegrationDto{user, forager}, activeOf("forager"), noDefault, registered)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "forager", got.Id)
	})

	t.Run("a disabled default is not chosen", func(t *testing.T) {
		got, _, err := pickServerReachable([]IntegrationDto{user, forager}, activeOf("forager"),
			func() (string, error) { return "user", nil }, registered)
		require.NoError(t, err)
		assert.Equal(t, "forager", got.Id)
	})

	t.Run("all candidates disabled keeps them, as a single disabled row is used", func(t *testing.T) {
		got, found, err := pickServerReachable([]IntegrationDto{user, forager}, activeOf(), noDefault, registered)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "user", got.Id)
	})

	// The log-field probe lists Loki labels for a form that has not been saved: its record
	// is synthetic, has no id, and is not in the account's active rows.
	t.Run("a row with no id was never persisted and is kept", func(t *testing.T) {
		synthetic := IntegrationDto{Source: "user", Type: "loki", Name: "loki"}
		got, found, err := pickServerReachable([]IntegrationDto{synthetic}, activeOf(), noDefault, registered)
		require.NoError(t, err)
		require.True(t, found, "a synthetic override row alone must be found")
		assert.Empty(t, got.Id)

		got, found, err = pickServerReachable([]IntegrationDto{synthetic, user}, activeOf("user"), noDefault, registered)
		require.NoError(t, err)
		require.True(t, found)
		assert.Empty(t, got.Id, "the unsaved form wins over a stored row of the same type")
	})

	t.Run("a single reachable row reads neither the active rows nor the default", func(t *testing.T) {
		never := func() (map[string]bool, error) {
			t.Fatal("must not read the active rows for one candidate")
			return nil, nil
		}
		neverDefault := func() (string, error) {
			t.Fatal("must not read the default for one candidate")
			return "", nil
		}
		got, found, err := pickServerReachable([]IntegrationDto{user, {Id: "k8s", Source: "agent", Name: "prometheus", Type: "prometheus"}}, never, neverDefault, registered)
		require.NoError(t, err)
		require.True(t, found)
		assert.Equal(t, "user", got.Id)
	})

	t.Run("a failed active-rows read is returned", func(t *testing.T) {
		_, _, err := pickServerReachable([]IntegrationDto{user, forager},
			func() (map[string]bool, error) { return nil, errors.New("connection reset") }, noDefault, registered)
		require.Error(t, err)
	})
}

// The exported entry point reads the account's active rows through listActiveIntegrations.
func TestPickServerReachableIntegration_ReadsActiveRowsForAChoice(t *testing.T) {
	user := IntegrationDto{Id: "user", Source: "user", Type: "loki"}
	other := IntegrationDto{Id: "other", Source: "user", Type: "loki"}

	orig := listActiveIntegrations
	t.Cleanup(func() { listActiveIntegrations = orig })
	calls := 0
	listActiveIntegrations = func(*security.RequestContext, string) ([]IntegrationDto, error) {
		calls++
		return nil, errors.New("connection reset")
	}

	_, _, err := PickServerReachableIntegration(nil, "acc", DefaultLogProvider, []IntegrationDto{user, other})
	require.Error(t, err, "a failed read of the active rows must surface")
	assert.Equal(t, 1, calls)

	_, found, err := PickServerReachableIntegration(nil, "acc", DefaultLogProvider, []IntegrationDto{user})
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, 1, calls, "a single reachable row must not read the active rows")
}

func TestPickServerReachable(t *testing.T) {
	user := IntegrationDto{Id: "user", Source: "user", Type: "prometheus"}
	k8sAgent := IntegrationDto{Id: "k8s", Source: "agent", Name: "prometheus", Type: "prometheus"}
	forager := IntegrationDto{Id: "forager", Source: "agent", Name: "vm-fleet-prom", Type: "prometheus"}
	registered := func(d IntegrationDto) (bool, error) { return d.Id == "forager", nil }

	pick := func(t *testing.T, defaultID string, dtos ...IntegrationDto) (string, bool) {
		t.Helper()
		got, found, err := pickServerReachable(dtos, activeOf("user", "forager", "k8s"), func() (string, error) { return defaultID, nil }, registered)
		require.NoError(t, err)
		return got.Id, found
	}

	t.Run("skips the Kubernetes agent's own row", func(t *testing.T) {
		id, found := pick(t, "", k8sAgent, user)
		assert.True(t, found)
		assert.Equal(t, "user", id)
		_, found = pick(t, "", k8sAgent)
		assert.False(t, found, "an agent-only account has nothing to connect to from the server")
		_, found = pick(t, "")
		assert.False(t, found)
	})

	t.Run("uses a forager-registered row when there is no user row", func(t *testing.T) {
		id, found := pick(t, "", k8sAgent, forager)
		assert.True(t, found)
		assert.Equal(t, "forager", id)
	})

	t.Run("prefers a user row over a forager-registered one when neither is the default", func(t *testing.T) {
		id, _ := pick(t, "", forager, user)
		assert.Equal(t, "user", id)
	})

	t.Run("the account's default wins when it is reachable", func(t *testing.T) {
		id, _ := pick(t, "forager", user, forager)
		assert.Equal(t, "forager", id, "the client must use the integration the resolver and status cards name")
		id, _ = pick(t, "user", forager, user)
		assert.Equal(t, "user", id)
	})

	t.Run("a default that cannot be reached from the server is ignored", func(t *testing.T) {
		id, _ := pick(t, "k8s", k8sAgent, user, forager)
		assert.Equal(t, "user", id)
	})

	t.Run("the default is looked up only when there is a choice to make", func(t *testing.T) {
		asked := 0
		defaultID := func() (string, error) { asked++; return "", nil }
		_, _, err := pickServerReachable([]IntegrationDto{user, k8sAgent}, activeOf("user"), defaultID, registered)
		require.NoError(t, err)
		assert.Zero(t, asked, "one reachable integration needs no default")
		_, _, err = pickServerReachable([]IntegrationDto{user, forager}, activeOf("user", "forager"), defaultID, registered)
		require.NoError(t, err)
		assert.Equal(t, 1, asked)
	})

	t.Run("a failed default lookup is returned, not read as no default", func(t *testing.T) {
		_, _, err := pickServerReachable([]IntegrationDto{user, forager}, activeOf("user", "forager"), func() (string, error) {
			return "", errors.New("connection reset")
		}, registered)
		require.Error(t, err)
	})

	t.Run("a failed registration lookup is returned", func(t *testing.T) {
		_, _, err := pickServerReachable([]IntegrationDto{forager}, activeOf(), noDefault, func(IntegrationDto) (bool, error) {
			return false, errors.New("boom")
		})
		require.Error(t, err)
	})
}
