package azure

import (
	"testing"
)

// TestServiceScope_FrontDoorClassic verifies Front Door Classic is global
func TestServiceScope_FrontDoorClassic(t *testing.T) {
	service := &frontDoorService{}

	if service.Scope() != ServiceScopeGlobal {
		t.Errorf("Expected Front Door Classic to have ServiceScopeGlobal, got %s", service.Scope())
	}
}

// TestServiceScope_FrontDoorStandardPremium verifies Front Door Standard/Premium is global
func TestServiceScope_FrontDoorStandardPremium(t *testing.T) {
	service := &frontDoorCdnService{}

	if service.Scope() != ServiceScopeGlobal {
		t.Errorf("Expected Front Door Standard/Premium to have ServiceScopeGlobal, got %s", service.Scope())
	}
}

// TestServiceScope_VM verifies VM is regional
func TestServiceScope_VM(t *testing.T) {
	service := &virtualMachineService{}

	if service.Scope() != ServiceScopeRegional {
		t.Errorf("Expected VM to have ServiceScopeRegional, got %s", service.Scope())
	}
}

// TestServiceScope_SQL verifies SQL is regional
func TestServiceScope_SQL(t *testing.T) {
	service := &sqlDatabaseService{}

	if service.Scope() != ServiceScopeRegional {
		t.Errorf("Expected SQL to have ServiceScopeRegional, got %s", service.Scope())
	}
}

// TestServiceScope_AllServices verifies all services have a valid scope.
func TestServiceScope_AllServices(t *testing.T) {
	// This map explicitly defines the expected scope for each service.
	// Services not in this map will be checked against the default (Regional).
	expectedScopes := map[string]ServiceScope{
		"microsoft.network/frontdoors":              ServiceScopeGlobal,
		"microsoft.cdn/profiles":                    ServiceScopeGlobal,
		"microsoft.network/dnszones":                ServiceScopeGlobal,
		"microsoft.authorization/roleassignments":   ServiceScopeGlobal,
		"microsoft.insights":                        ServiceScopeGlobal,
		"microsoft.authorization/policyassignments": ServiceScopeGlobal,
		"microsoft.securityinsights":                ServiceScopeGlobal,
		"microsoft.devops/projects":                 ServiceScopeGlobal,
		"microsoft.devops/pipelines":                ServiceScopeGlobal,
		"microsoft.security/pricings":               ServiceScopeGlobal,
	}

	if azureServiceMap == nil {
		t.Fatal("azureServiceMap not initialized. Ensure init() in main.go has run.")
	}

	for name, service := range azureServiceMap {
		t.Run(name, func(t *testing.T) {
			scope := service.Scope()

			// Verify scope is one of the valid values
			if scope != ServiceScopeGlobal &&
				scope != ServiceScopeRegional &&
				scope != ServiceScopeSubscription {
				t.Errorf("Service %s has invalid scope: %s", service.Name(), scope)
			}

			expectedScope, isExplicitlyDefined := expectedScopes[name]
			if isExplicitlyDefined {
				if scope != expectedScope {
					t.Errorf("Service %s has scope %s, want %s", name, scope, expectedScope)
				}
			} else {
				// Assume services not explicitly listed as global should be regional.
				if scope != ServiceScopeRegional {
					t.Errorf("Service %s has non-default scope %s but is not in expectedScopes map", name, scope)
				}
			}
		})
	}
}
