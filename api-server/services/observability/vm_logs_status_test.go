package observability

import (
	"errors"
	"testing"
	"time"

	"nudgebee/services/account"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type labelsFn = func(*security.RequestContext, string, string, string, time.Time) ([]OutputLogLabel, error)

func stubVMLogs(t *testing.T, resolve resolveFn, probe func(*security.RequestContext, string, string) error, labels labelsFn) {
	t.Helper()
	oResolve, oProbe, oLabels := resolveProviderForStatus, probeIntegrationForAccount, listRecentLogLabels
	t.Cleanup(func() {
		resolveProviderForStatus, probeIntegrationForAccount, listRecentLogLabels = oResolve, oProbe, oLabels
	})
	resolveProviderForStatus, probeIntegrationForAccount, listRecentLogLabels = resolve, probe, labels
}

func resolveUserLoki(*security.RequestContext, string, string, string, string) (string, string, *core.IntegrationDto, error) {
	return "loki", "user", userProm(), nil
}

func TestCheckVMLogsStatus_RequiresAccount(t *testing.T) {
	_, err := CheckVMLogsStatus(nil, "")
	require.Error(t, err)
}

func TestCheckVMLogsStatus_States(t *testing.T) {
	labels := func(ls ...string) labelsFn {
		return func(*security.RequestContext, string, string, string, time.Time) ([]OutputLogLabel, error) {
			out := []OutputLogLabel{}
			for _, l := range ls {
				out = append(out, OutputLogLabel{Label: l})
			}
			return out, nil
		}
	}

	t.Run("no integration is not_configured", func(t *testing.T) {
		stubVMLogs(t, func(*security.RequestContext, string, string, string, string) (string, string, *core.IntegrationDto, error) {
			return "", "", nil, account.ErrAgentNotConnected
		}, noProbe(t), labels())
		st, err := CheckVMLogsStatus(nil, "acc")
		require.NoError(t, err)
		assert.Equal(t, VMLogsNotConfigured, st.State)
	})

	t.Run("a Kubernetes agent's loki is not_configured", func(t *testing.T) {
		stubVMLogs(t, func(*security.RequestContext, string, string, string, string) (string, string, *core.IntegrationDto, error) {
			return "loki", "agent", userProm(), nil
		}, noProbe(t), labels("app"))
		st, _ := CheckVMLogsStatus(nil, "acc")
		assert.Equal(t, VMLogsNotConfigured, st.State)
	})

	t.Run("resolver failure is check_failed, not not_configured", func(t *testing.T) {
		stubVMLogs(t, func(*security.RequestContext, string, string, string, string) (string, string, *core.IntegrationDto, error) {
			return "", "", nil, errors.New("several providers, none default")
		}, noProbe(t), labels())
		st, _ := CheckVMLogsStatus(nil, "acc")
		assert.Equal(t, VMLogsCheckFailed, st.State)
		assert.Contains(t, st.Error, "none default")
	})

	t.Run("query answers with streams is connected and skips the probe", func(t *testing.T) {
		stubVMLogs(t, resolveUserLoki, noProbe(t), labels("host", "service_name"))
		st, _ := CheckVMLogsStatus(nil, "acc")
		assert.Equal(t, VMLogsConnected, st.State)
		assert.Equal(t, "loki", st.Provider)
		assert.Equal(t, "int-1", st.IntegrationId)
	})

	t.Run("reachable but empty is no_data", func(t *testing.T) {
		stubVMLogs(t, resolveUserLoki, noProbe(t), labels())
		st, _ := CheckVMLogsStatus(nil, "acc")
		assert.Equal(t, VMLogsNoData, st.State)
	})

	t.Run("query failure is unreachable and the probe's error wins", func(t *testing.T) {
		stubVMLogs(t, resolveUserLoki,
			func(_ *security.RequestContext, integrationID, accountID string) error {
				assert.Equal(t, "int-1", integrationID)
				assert.Equal(t, "acc", accountID, "the probe must run through this account's forager")
				return errors.New("forager cannot reach loki")
			},
			func(*security.RequestContext, string, string, string, time.Time) ([]OutputLogLabel, error) {
				return nil, errors.New("query failed")
			})
		st, _ := CheckVMLogsStatus(nil, "acc")
		assert.Equal(t, VMLogsUnreachable, st.State)
		assert.Equal(t, "forager cannot reach loki", st.Error)
	})
}
