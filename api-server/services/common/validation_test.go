package common

import "testing"

func TestIsValidK8sDNSLabel(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "simple lowercase", in: "myapp", want: true},
		{name: "with hyphen and digits", in: "my-app-1", want: true},
		{name: "single char", in: "a", want: true},
		{name: "exactly 63 chars", in: "012345678901234567890123456789012345678901234567890123456789012", want: true},
		{name: "over 63 chars rejected", in: "0123456789012345678901234567890123456789012345678901234567890123", want: false},
		{name: "uppercase rejected", in: "MyApp", want: false},
		{name: "leading hyphen rejected", in: "-app", want: false},
		{name: "trailing hyphen rejected", in: "app-", want: false},
		{name: "underscore rejected", in: "my_app", want: false},
		{name: "empty rejected", in: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidK8sDNSLabel(tt.in); got != tt.want {
				t.Errorf("IsValidK8sDNSLabel(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsValidK8sAccountName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "simple name", in: "acme", want: true},
		{name: "with spaces and digits", in: "Acme Corp 1", want: true},
		{name: "underscore and hyphen allowed", in: "acme_corp-1", want: true},
		{name: "exactly 40 chars", in: "0123456789012345678901234567890123456789", want: true},
		{name: "over 40 chars rejected", in: "01234567890123456789012345678901234567890", want: false},
		{name: "leading space rejected", in: " acme", want: false},
		{name: "empty rejected", in: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidK8sAccountName(tt.in); got != tt.want {
				t.Errorf("IsValidK8sAccountName(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestIsValidUserEmail(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "standard email", in: "user@example.com", want: true},
		{name: "subdomain and plus tag", in: "user.name+tag@mail.example.co", want: true},
		{name: "missing @ rejected", in: "userexample.com", want: false},
		{name: "missing domain tld rejected", in: "user@example", want: false},
		{name: "leading space rejected", in: " user@example.com", want: false},
		{name: "empty rejected", in: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidUserEmail(tt.in); got != tt.want {
				t.Errorf("IsValidUserEmail(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
