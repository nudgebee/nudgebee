package integrations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nudgebee/services/integrations/core"
)

func incidentIOValues(url, apiKey string) []core.IntegrationConfigValue {
	return []core.IntegrationConfigValue{
		{Name: core.IntegrationConfigName, Value: "incidentio-prod"},
		{Name: IncidentIOConfigUrl, Value: url},
		{Name: IncidentIOConfigPassword, Value: apiKey},
	}
}

func TestIncidentIO_Metadata(t *testing.T) {
	i := IncidentIO{}
	if i.Name() != IntegrationIncidentIO {
		t.Errorf("Name() = %q, want %q", i.Name(), IntegrationIncidentIO)
	}
	if i.Name() != "incidentio" {
		t.Errorf("Name() = %q — must match the ticket_tool_types enum value", i.Name())
	}
	if i.Category() != core.IntegrationCategoryTicketing {
		t.Errorf("Category() = %v, want ticketing", i.Category())
	}

	schema := i.ConfigSchema()
	if _, ok := schema.Properties[IncidentIOConfigPassword]; !ok {
		t.Fatal("ConfigSchema missing the api key property")
	}
	if !schema.Properties[IncidentIOConfigPassword].IsEncrypted {
		t.Error("the incident.io api key must be stored encrypted")
	}
	requiresKey := false
	for _, r := range schema.Required {
		if r == IncidentIOConfigPassword {
			requiresKey = true
		}
	}
	if !requiresKey {
		t.Error("the api key must be a required field")
	}
}

func TestIncidentIO_ValidateConfig(t *testing.T) {
	t.Run("missing api key", func(t *testing.T) {
		errs := IncidentIO{}.ValidateConfig(nil, incidentIOValues("https://api.incident.io", ""), "")
		if len(errs) == 0 {
			t.Fatal("ValidateConfig() should reject an empty api key")
		}
	})

	t.Run("valid key passes and uses bearer auth", func(t *testing.T) {
		var gotAuth, gotPath string
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"severities":[{"id":"01SEV","name":"Minor"}]}`))
		}))
		defer stub.Close()

		errs := IncidentIO{}.ValidateConfig(nil, incidentIOValues(stub.URL, "key-123"), "")
		if len(errs) != 0 {
			t.Fatalf("ValidateConfig() errors = %v, want none", errs)
		}
		if gotAuth != "Bearer key-123" {
			t.Errorf("Authorization = %q, want Bearer key-123", gotAuth)
		}
		if gotPath != "/v1/severities" {
			t.Errorf("probe path = %q, want /v1/severities", gotPath)
		}
	})

	t.Run("401 reports an auth failure", func(t *testing.T) {
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		}))
		defer stub.Close()

		errs := IncidentIO{}.ValidateConfig(nil, incidentIOValues(stub.URL, "bad-key"), "")
		if len(errs) == 0 {
			t.Fatal("ValidateConfig() should reject a 401")
		}
		if !strings.Contains(strings.ToLower(errs[0].Error()), "authentication") {
			t.Errorf("error = %q, want it to name an authentication failure", errs[0])
		}
	})
}

func TestIncidentIO_ListUsers(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/users" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"users":[
			{"id":"01USER_A","name":"Lisa Curtis","email":"lisa@example.com"},
			{"id":"01USER_B","name":"Sam Reed","email":"sam@example.com"}]}`))
	}))
	defer stub.Close()

	users, err := IncidentIO{}.ListUsers(context.Background(), incidentIOValues(stub.URL, "key-123"))
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("got %d users, want 2", len(users))
	}
	if users[0].Email != "lisa@example.com" || users[0].DisplayName != "Lisa Curtis" {
		t.Errorf("user[0] = %+v, want lisa@example.com / Lisa Curtis", users[0])
	}
	if users[0].ID != "01USER_A" {
		t.Errorf("user[0].ID = %q, want the incident.io user ULID", users[0].ID)
	}
}
