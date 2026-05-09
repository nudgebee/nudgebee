package azure

import (
	"testing"
)

// TestStringManipulation tests string helper functions
func TestContains(t *testing.T) {
	tests := []struct {
		s      string
		substr string
		want   bool
	}{
		{"Microsoft.Compute/virtualMachines", "Compute", true},
		{"Microsoft.Compute/virtualMachines", "Storage", false},
		{"", "", true},
		{"test", "", true},
		{"", "test", false},
	}

	for _, tt := range tests {
		got := contains(tt.s, tt.substr)
		if got != tt.want {
			t.Errorf("contains(%q, %q) = %v, want %v", tt.s, tt.substr, got, tt.want)
		}
	}
}

func TestHasPrefix(t *testing.T) {
	tests := []struct {
		s      string
		prefix string
		want   bool
	}{
		{"Microsoft.Compute/virtualMachines", "Microsoft", true},
		{"Microsoft.Compute/virtualMachines", "Azure", false},
		{"", "", true},
		{"test", "", true},
		{"", "test", false},
	}

	for _, tt := range tests {
		got := hasPrefix(tt.s, tt.prefix)
		if got != tt.want {
			t.Errorf("hasPrefix(%q, %q) = %v, want %v", tt.s, tt.prefix, got, tt.want)
		}
	}
}

func TestHasSuffix(t *testing.T) {
	tests := []struct {
		s      string
		suffix string
		want   bool
	}{
		{"Microsoft.Compute/virtualMachines/write", "/write", true},
		{"Microsoft.Compute/virtualMachines/write", "/delete", false},
		{"", "", true},
		{"test", "", true},
		{"", "test", false},
	}

	for _, tt := range tests {
		got := hasSuffix(tt.s, tt.suffix)
		if got != tt.want {
			t.Errorf("hasSuffix(%q, %q) = %v, want %v", tt.s, tt.suffix, got, tt.want)
		}
	}
}

func TestToLower(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"EastUS", "eastus"},
		{"WESTEUROPE", "westeurope"},
		{"alreadylower", "alreadylower"},
		{"MixedCase123", "mixedcase123"},
		{"", ""},
	}

	for _, tt := range tests {
		got := toLower(tt.input)
		if got != tt.want {
			t.Errorf("toLower(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToUpper(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"eastus", "EASTUS"},
		{"westeurope", "WESTEUROPE"},
		{"ALREADYUPPER", "ALREADYUPPER"},
		{"MixedCase123", "MIXEDCASE123"},
		{"", ""},
	}

	for _, tt := range tests {
		got := toUpper(tt.input)
		if got != tt.want {
			t.Errorf("toUpper(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTrim(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"  test  ", "test"},
		{"\ttest\n", "test"},
		{"  \n\ttest\r\n  ", "test"},
		{"nowhitespace", "nowhitespace"},
		{"", ""},
	}

	for _, tt := range tests {
		got := trim(tt.input)
		if got != tt.want {
			t.Errorf("trim(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSplit(t *testing.T) {
	tests := []struct {
		s    string
		sep  string
		want []string
	}{
		{"/subscriptions/12345/resourceGroups/rg", "/", []string{"", "subscriptions", "12345", "resourceGroups", "rg"}},
		{"a,b,c", ",", []string{"a", "b", "c"}},
		{"test", ",", []string{"test"}},
		{"", ",", []string{""}},
	}

	for _, tt := range tests {
		got := split(tt.s, tt.sep)
		if len(got) != len(tt.want) {
			t.Errorf("split(%q, %q) returned %d parts, want %d", tt.s, tt.sep, len(got), len(tt.want))
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("split(%q, %q)[%d] = %q, want %q", tt.s, tt.sep, i, got[i], tt.want[i])
			}
		}
	}
}

func TestJoin(t *testing.T) {
	tests := []struct {
		elems []string
		sep   string
		want  string
	}{
		{[]string{"a", "b", "c"}, ",", "a,b,c"},
		{[]string{"subscriptions", "12345", "resourceGroups"}, "/", "subscriptions/12345/resourceGroups"},
		{[]string{"test"}, ",", "test"},
		{[]string{}, ",", ""},
	}

	for _, tt := range tests {
		got := join(tt.elems, tt.sep)
		if got != tt.want {
			t.Errorf("join(%v, %q) = %q, want %q", tt.elems, tt.sep, got, tt.want)
		}
	}
}

func TestReplace(t *testing.T) {
	tests := []struct {
		s    string
		old  string
		new  string
		want string
	}{
		{"East US", " ", "", "EastUS"},
		{"test-value", "-", "_", "test_value"},
		{"aaabbbccc", "b", "x", "aaaxxxccc"},
		{"test", "z", "y", "test"},
		{"", "a", "b", ""},
	}

	for _, tt := range tests {
		got := replace(tt.s, tt.old, tt.new)
		if got != tt.want {
			t.Errorf("replace(%q, %q, %q) = %q, want %q", tt.s, tt.old, tt.new, got, tt.want)
		}
	}
}

// TestLogicalOperations tests comparison and logical functions
func TestEq(t *testing.T) {
	tests := []struct {
		a    interface{}
		b    interface{}
		want bool
	}{
		{"test", "test", true},
		{"test", "other", false},
		{123, 123, true},
		{123, 456, false},
		{true, true, true},
		{true, false, false},
		{nil, nil, true},
	}

	for _, tt := range tests {
		got := eq(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("eq(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNe(t *testing.T) {
	tests := []struct {
		a    interface{}
		b    interface{}
		want bool
	}{
		{"test", "test", false},
		{"test", "other", true},
		{123, 123, false},
		{123, 456, true},
		{true, true, false},
		{true, false, true},
	}

	for _, tt := range tests {
		got := ne(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("ne(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestAnd(t *testing.T) {
	tests := []struct {
		args []bool
		want bool
	}{
		{[]bool{true, true, true}, true},
		{[]bool{true, false, true}, false},
		{[]bool{false, false, false}, false},
		{[]bool{true}, true},
		{[]bool{false}, false},
		{[]bool{}, true}, // Empty AND is true
	}

	for _, tt := range tests {
		got := and(tt.args...)
		if got != tt.want {
			t.Errorf("and(%v) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func TestOr(t *testing.T) {
	tests := []struct {
		args []bool
		want bool
	}{
		{[]bool{true, true, true}, true},
		{[]bool{true, false, true}, true},
		{[]bool{false, false, false}, false},
		{[]bool{true}, true},
		{[]bool{false}, false},
		{[]bool{}, false}, // Empty OR is false
	}

	for _, tt := range tests {
		got := or(tt.args...)
		if got != tt.want {
			t.Errorf("or(%v) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func TestNot(t *testing.T) {
	tests := []struct {
		input bool
		want  bool
	}{
		{true, false},
		{false, true},
	}

	for _, tt := range tests {
		got := not(tt.input)
		if got != tt.want {
			t.Errorf("not(%v) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

// TestTypeConversion tests type conversion helpers
func TestToString(t *testing.T) {
	tests := []struct {
		input interface{}
		want  string
	}{
		{"test", "test"},
		{123, "123"},
		{true, "true"},
		{12.34, "12.34"},
		{nil, ""},
	}

	for _, tt := range tests {
		got := toString(tt.input)
		if got != tt.want {
			t.Errorf("toString(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestToInt(t *testing.T) {
	tests := []struct {
		input     interface{}
		want      int
		wantError bool
	}{
		{123, 123, false},
		{int64(456), 456, false},
		{float64(789), 789, false},
		{"123", 123, false},
		{"invalid", 0, true},
		{true, 0, true},
	}

	for _, tt := range tests {
		got, err := toInt(tt.input)
		if tt.wantError {
			if err == nil {
				t.Errorf("toInt(%v) expected error, got nil", tt.input)
			}
		} else {
			if err != nil {
				t.Errorf("toInt(%v) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("toInt(%v) = %d, want %d", tt.input, got, tt.want)
			}
		}
	}
}

// TestExtractResourceName tests resource name extraction
func TestExtractResourceName(t *testing.T) {
	tests := []struct {
		resourceId string
		want       string
	}{
		{
			"/subscriptions/12345/resourceGroups/test-rg/providers/Microsoft.Compute/virtualMachines/test-vm",
			"test-vm",
		},
		{
			"/subscriptions/12345/resourceGroups/test-rg/providers/Microsoft.Storage/storageAccounts/mystorageacct",
			"mystorageacct",
		},
		{
			"/subscriptions/12345/resourceGroups/rg/providers/Microsoft.Sql/servers/myserver/databases/mydb",
			"mydb",
		},
		{
			"test-vm",
			"test-vm",
		},
		{
			"/test/path/",
			"path",
		},
		{
			"",
			"",
		},
	}

	for _, tt := range tests {
		got := extractResourceName(tt.resourceId)
		if got != tt.want {
			t.Errorf("extractResourceName(%q) = %q, want %q", tt.resourceId, got, tt.want)
		}
	}
}

// TestAzureResourceIDParsing tests complete Azure resource ID parsing
func TestAzureResourceIDParsing(t *testing.T) {
	tests := []struct {
		name         string
		resourceId   string
		wantSub      string
		wantRG       string
		wantProvider string
		wantType     string
		wantName     string
	}{
		{
			name:         "Complete VM resource ID",
			resourceId:   "/subscriptions/12345/resourceGroups/test-rg/providers/Microsoft.Compute/virtualMachines/test-vm",
			wantSub:      "12345",
			wantRG:       "test-rg",
			wantProvider: "Microsoft.Compute",
			wantType:     "virtualMachines",
			wantName:     "test-vm",
		},
		{
			name:         "Storage account",
			resourceId:   "/subscriptions/67890/resourceGroups/storage-rg/providers/Microsoft.Storage/storageAccounts/mystorageacct",
			wantSub:      "67890",
			wantRG:       "storage-rg",
			wantProvider: "Microsoft.Storage",
			wantType:     "storageAccounts",
			wantName:     "mystorageacct",
		},
		{
			name:         "Nested resource (SQL database)",
			resourceId:   "/subscriptions/12345/resourceGroups/db-rg/providers/Microsoft.Sql/servers/myserver/databases/mydb",
			wantSub:      "12345",
			wantRG:       "db-rg",
			wantProvider: "Microsoft.Sql",
			wantType:     "servers",  // parseAzureResourceID extracts first type segment
			wantName:     "myserver", // parseAzureResourceID extracts first name segment
		},
		{
			name:         "Empty resource ID",
			resourceId:   "",
			wantSub:      "",
			wantRG:       "",
			wantProvider: "",
			wantType:     "",
			wantName:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSub, gotRG, gotProvider, gotType, gotName := parseAzureResourceID(tt.resourceId)

			if gotSub != tt.wantSub {
				t.Errorf("subscription: got %q, want %q", gotSub, tt.wantSub)
			}
			if gotRG != tt.wantRG {
				t.Errorf("resourceGroup: got %q, want %q", gotRG, tt.wantRG)
			}
			if gotProvider != tt.wantProvider {
				t.Errorf("provider: got %q, want %q", gotProvider, tt.wantProvider)
			}
			if gotType != tt.wantType {
				t.Errorf("resourceType: got %q, want %q", gotType, tt.wantType)
			}
			if gotName != tt.wantName {
				t.Errorf("resourceName: got %q, want %q", gotName, tt.wantName)
			}
		})
	}
}

// TestHelperFunctionIntegration tests how helper functions work together in templates
func TestHelperFunctionIntegration(t *testing.T) {
	// Test scenario: extract and normalize a resource name from a resource ID
	resourceId := "/subscriptions/12345/resourceGroups/Test-RG/providers/Microsoft.Compute/virtualMachines/MyVM"

	// Extract resource name
	name := extractResourceName(resourceId)
	if name != "MyVM" {
		t.Errorf("Expected resource name 'MyVM', got '%s'", name)
	}

	// Normalize to lowercase (as used in templates)
	normalizedName := toLower(name)
	if normalizedName != "myvm" {
		t.Errorf("Expected normalized name 'myvm', got '%s'", normalizedName)
	}

	// Test scenario: check if resource is a VM and extract components
	_, _, provider, resourceType, _ := parseAzureResourceID(resourceId)

	if !contains(provider, "Compute") {
		t.Errorf("Expected provider to contain 'Compute'")
	}

	if resourceType != "virtualMachines" {
		t.Errorf("Expected resource type 'virtualMachines', got '%s'", resourceType)
	}

	// Build service name (provider + type in lowercase)
	serviceName := toLower(provider + "/" + resourceType)
	expectedServiceName := "microsoft.compute/virtualmachines"
	if serviceName != expectedServiceName {
		t.Errorf("Expected service name '%s', got '%s'", expectedServiceName, serviceName)
	}

	// Test scenario: check multiple conditions (AND/OR logic)
	isWrite := hasSuffix(resourceId, "/write")
	isVM := contains(resourceId, "virtualMachines")
	isComputeResource := contains(resourceId, "Microsoft.Compute")

	if and(isVM, isComputeResource) != true {
		t.Errorf("Expected VM and Compute to both be true")
	}

	if or(isWrite, isVM) != true {
		t.Errorf("Expected at least one of write or VM to be true")
	}

	if not(isWrite) != true {
		t.Errorf("Expected NOT isWrite to be true")
	}
}
