package integrations

import (
	"testing"

	"nudgebee/services/integrations/core"

	"github.com/stretchr/testify/assert"
)

func TestSignozConfigSchema_ConnectionMode(t *testing.T) {
	p := Signoz{}.ConfigSchema().Properties[SignozConnectionModeKey]
	assert.Equal(t, SignozConnectionModeDirect, p.Default, "rows saved before the field existed are direct")
	assert.Equal(t, []any{SignozConnectionModeDirect, SignozConnectionModeVMAgent}, p.Enum)
}

// A vm_agent SigNoz is only reachable from the customer's network, so validation must
// not probe it: no httptest server exists here, and a probe would fail with a dial error.
func TestSignozValidateConfig_VMAgent(t *testing.T) {
	values := func(over map[string]string) []core.IntegrationConfigValue {
		m := map[string]string{
			SignozConnectionModeKey: SignozConnectionModeVMAgent,
			"signoz_mode":           SignozModeSelfHosted,
			"signoz_url":            "http://10.0.0.9:3301",
			"signoz_username":       "ops@corp.example",
			"signoz_password":       "pw",
		}
		for k, v := range over {
			m[k] = v
		}
		out := make([]core.IntegrationConfigValue, 0, len(m))
		for k, v := range m {
			out = append(out, core.IntegrationConfigValue{Name: k, Value: v})
		}
		return out
	}

	t.Run("a private address is accepted without a probe", func(t *testing.T) {
		assert.Empty(t, Signoz{}.ValidateConfig(nil, values(nil), "acc"))
	})
	t.Run("SigNoz Cloud is rejected", func(t *testing.T) {
		assert.NotEmpty(t, Signoz{}.ValidateConfig(nil, values(map[string]string{"signoz_mode": SignozModeCloud}), "acc"))
	})
	t.Run("credentials and url are still required", func(t *testing.T) {
		assert.NotEmpty(t, Signoz{}.ValidateConfig(nil, values(map[string]string{"signoz_password": ""}), "acc"))
		assert.NotEmpty(t, Signoz{}.ValidateConfig(nil, values(map[string]string{"signoz_url": ""}), "acc"))
		assert.NotEmpty(t, Signoz{}.ValidateConfig(nil, values(map[string]string{"signoz_url": "http://10.0.0.9:3301/x"}), "acc"))
	})
}
