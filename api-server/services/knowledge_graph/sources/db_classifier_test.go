package sources

import "testing"

func TestClassifyDatastore(t *testing.T) {
	cases := []struct {
		name       string
		framework  string
		wantEngine string
		wantRole   string
		wantOk     bool
	}{
		// Databases (framework value from cloud_resource_attributes).
		{"postgres", "postgres", "postgres", roleDatabase, true},
		// Canonicalization: a "postgresql" value normalizes to engine "postgres".
		{"postgresql variant", "postgresql", "postgres", roleDatabase, true},
		{"mongodb", "mongodb", "mongodb", roleDatabase, true},
		{"clickhouse", "clickhouse", "clickhouse", roleDatabase, true},
		{"opensearch", "opensearch", "opensearch", roleDatabase, true},
		// Caches.
		{"redis cache", "redis", "redis", roleCache, true},
		{"valkey cache", "valkey", "valkey", roleCache, true},
		{"memcached cache", "memcached", "memcached", roleCache, true},
		// Message queues.
		{"rabbitmq queue", "rabbitmq", "rabbitmq", roleMessageQueue, true},
		{"kafka queue", "kafka", "kafka", roleMessageQueue, true},
		// Coordination stores are intentionally unclassified.
		{"zookeeper unclassified", "zookeeper", "", "", false},
		{"etcd unclassified", "etcd", "", "", false},
		// Non-datastore frameworks and empty values are not classified.
		{"app framework", "golang", "", "", false},
		{"nginx framework", "nginx", "", "", false},
		{"empty", "", "", "", false},
	}
	for _, c := range cases {
		eng, role, ok := classifyDatastore(c.framework)
		if eng != c.wantEngine || role != c.wantRole || ok != c.wantOk {
			t.Errorf("%s: classifyDatastore(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.name, c.framework, eng, role, ok, c.wantEngine, c.wantRole, c.wantOk)
		}
	}
}
