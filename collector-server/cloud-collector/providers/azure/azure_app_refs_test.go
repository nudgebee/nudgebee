package azure

import "testing"

func TestAzureExtractResourceRefs(t *testing.T) {
	values := []string{
		"DefaultEndpointsProtocol=https;AccountName=myfuncstore;AccountKey=SECRETKEY==;EndpointSuffix=core.windows.net",
		"@Microsoft.KeyVault(SecretUri=https://prod-kv.vault.azure.net/secrets/db-pass/)",
		"Server=tcp:sql-server-prod.database.windows.net,1433;Password=SUPERSECRET;",
		"host=pgsql-cgc-awb-prod.postgres.database.azure.com port=5432 password=nope",
		"my-redis-cache.redis.cache.windows.net:6380,password=REDISKEY=,ssl=True",
		"Endpoint=sb://orders-bus.servicebus.windows.net/;SharedAccessKey=ABC=",
		"https://vision-svc.cognitiveservices.azure.com/",
		"AppInsightsKey=00000000-1111-2222-3333-444455556666", // no resource → ignored
		"SomeFlag=true",
	}
	refs := azureExtractResourceRefs(values)

	got := map[string]string{}
	for _, r := range refs {
		got[r["kind"]] = r["name"]
	}
	want := map[string]string{
		"storage":    "myfuncstore",
		"keyvault":   "prod-kv",
		"sql":        "sql-server-prod",
		"postgres":   "pgsql-cgc-awb-prod",
		"redis":      "my-redis-cache",
		"servicebus": "orders-bus",
		"cognitive":  "vision-svc",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("kind %q: got name %q, want %q", k, got[k], v)
		}
	}
	// Security: NO secret material may appear in any extracted name.
	for _, r := range refs {
		for _, secret := range []string{"SECRETKEY", "SUPERSECRET", "REDISKEY", "SharedAccessKey", "db-pass", "password"} {
			if containsFold(r["name"], secret) {
				t.Errorf("extracted ref %q leaked secret-ish token %q", r["name"], secret)
			}
		}
	}
}

func containsFold(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (indexFold(s, sub) >= 0)
}
func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			cs, ct := s[i+j], sub[j]
			if 'A' <= cs && cs <= 'Z' {
				cs += 32
			}
			if 'A' <= ct && ct <= 'Z' {
				ct += 32
			}
			if cs != ct {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
