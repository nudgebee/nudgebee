package flow_sources

import "testing"

func TestParseEBPFServerName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantOK  bool
		wantIP  string
		wantPrt string
		wantFQ  string
	}{
		{
			name:    "well-formed argocd-redis target",
			input:   "34.118.228.211/6379/argocd-redis.argocd.svc.cluster.local",
			wantOK:  true,
			wantIP:  "34.118.228.211",
			wantPrt: "6379",
			wantFQ:  "argocd-redis.argocd.svc.cluster.local",
		},
		{
			name:    "well-formed kube-dns target",
			input:   "34.118.224.10/53/kube-dns.kube-system.svc.cluster.local",
			wantOK:  true,
			wantIP:  "34.118.224.10",
			wantPrt: "53",
			wantFQ:  "kube-dns.kube-system.svc.cluster.local",
		},
		{
			name:    "FQDN containing slashes is preserved verbatim",
			input:   "10.0.0.1/443/foo/bar/baz",
			wantOK:  true,
			wantIP:  "10.0.0.1",
			wantPrt: "443",
			wantFQ:  "foo/bar/baz",
		},
		{
			name:   "two segments — rejected",
			input:  "34.118.228.211/6379",
			wantOK: false,
		},
		{
			name:   "one segment — rejected",
			input:  "34.118.228.211",
			wantOK: false,
		},
		{
			name:   "empty input — rejected",
			input:  "",
			wantOK: false,
		},
		{
			name:   "empty IP segment — rejected",
			input:  "/6379/foo.svc",
			wantOK: false,
		},
		{
			name:   "empty port segment — rejected",
			input:  "10.0.0.1//foo.svc",
			wantOK: false,
		},
		{
			name:   "empty FQDN segment — rejected",
			input:  "10.0.0.1/443/",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseEBPFServerName(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok: want %v, got %v", tt.wantOK, ok)
			}
			if !ok {
				return
			}
			if got.IP != tt.wantIP {
				t.Errorf("IP: want %q, got %q", tt.wantIP, got.IP)
			}
			if got.Port != tt.wantPrt {
				t.Errorf("Port: want %q, got %q", tt.wantPrt, got.Port)
			}
			if got.FQDN != tt.wantFQ {
				t.Errorf("FQDN: want %q, got %q", tt.wantFQ, got.FQDN)
			}
		})
	}
}
