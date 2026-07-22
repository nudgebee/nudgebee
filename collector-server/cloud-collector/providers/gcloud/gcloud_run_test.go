package gcloud

import "testing"

func TestRedisHostFromEnv(t *testing.T) {
	cases := []struct{ name, val, want string }{
		{"CBP_RESPONSE_CACHE_REDIS_URL", "10.193.205.204:6379", "10.193.205.204"},
		{"SOME_URL", "10.40.243.4:6379", "10.40.243.4"},                 // matched via :6379
		{"REDIS_HOST", "redis://user:pass@10.1.2.3:6379/0", "10.1.2.3"}, // scheme+creds+path stripped
		{"CACHE_HOST", "cache.internal.example.com", "cache.internal.example.com"},
		{"REDIS_ENABLED", "true", ""}, // non-host value rejected
		{"REDIS_DB", "0", ""},         // bare number rejected
		{"UNRELATED", "hello", ""},    // not redis-named, no port → ignored
		{"REDIS_SECRET", "", ""},      // empty (secretKeyRef) → ignored
	}
	for _, c := range cases {
		if got := redisHostFromEnv(c.name, c.val); got != c.want {
			t.Errorf("redisHostFromEnv(%q,%q)=%q want %q", c.name, c.val, got, c.want)
		}
	}
}
