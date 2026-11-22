package providers

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
)

// RedactedValue replaces a secret value in a resource's Meta. Keys and structure
// are kept, so readers still see which variables or settings exist.
const RedactedValue = "[redacted]"

// Most collectors store the whole provider object as Meta (structToMap), and
// some of those objects carry secret values the provider API returns: Lambda
// and container environment variables, origin and BGP auth keys, startup
// scripts. Meta is readable by every role that can read cloud resources and by
// the AI tools, so those values must never be stored.
//
// RedactMetaSecrets rewrites meta in place. It is idempotent, so a path that
// feeds already-stored meta back through it (the Azure realtime update reads the
// row's meta back) is safe.
//
// Environment variables that are references to other resources (ARNs, SQS queue
// URLs, a DynamoDB table name) are kept: the knowledge graph links a Lambda to
// its queue, topic and table from them, and they are identifiers, not secrets.
func RedactMetaSecrets(meta map[string]any) {
	if meta == nil {
		return
	}
	redactSecretKeys(meta)

	// AWS Lambda: Environment.Variables{KEY: value}.
	if env, ok := meta["Environment"].(map[string]any); ok {
		switch vars := env["Variables"].(type) {
		case map[string]any:
			for k, v := range vars {
				if s, ok := v.(string); ok && !isResourceReference(k, s) {
					vars[k] = RedactedValue
				}
			}
		case map[string]string:
			for k, s := range vars {
				if !isResourceReference(k, s) {
					vars[k] = RedactedValue
				}
			}
		}
	}

	// AWS ECS / Fargate task: Overrides.ContainerOverrides[].Environment[]{Name, Value}.
	if overrides, ok := meta["Overrides"].(map[string]any); ok {
		for _, c := range asMaps(overrides["ContainerOverrides"]) {
			redactEnvList(c["Environment"], "Name", "Value")
		}
	}

	// AWS ECS task definition (event evidence): ContainerDefinitions[].Environment[]{Name, Value}.
	for _, c := range asMaps(meta["ContainerDefinitions"]) {
		redactEnvList(c["Environment"], "Name", "Value")
	}

	// AWS CloudFront: DistributionConfig.Origins.Items[].CustomHeaders.Items[].HeaderValue
	// (typically the shared secret the origin checks).
	if cfg, ok := meta["DistributionConfig"].(map[string]any); ok {
		if origins, ok := cfg["Origins"].(map[string]any); ok {
			for _, origin := range asMaps(origins["Items"]) {
				if headers, ok := origin["CustomHeaders"].(map[string]any); ok {
					for _, h := range asMaps(headers["Items"]) {
						redactString(h, "HeaderValue")
					}
				}
			}
		}
	}

	if props, ok := meta["properties"].(map[string]any); ok {
		// Azure Container Apps: properties.template.(init)containers[].env[]{name, value}.
		if tmpl, ok := props["template"].(map[string]any); ok {
			for _, list := range []string{"containers", "initContainers"} {
				for _, c := range asMaps(tmpl[list]) {
					redactEnvList(c["env"], "name", "value")
				}
			}
		}
		// Azure ExpressRoute circuit: the service key that authorizes provisioning.
		redactString(props, "serviceKey")
	}

	// Azure Monitor action group: receiver URLs carry the key in the query
	// string (function `?code=`, logic app `sig=`).
	for _, list := range []string{"webhookReceivers", "azureFunctionReceivers", "logicAppReceivers", "automationRunbookReceivers"} {
		for _, r := range asMaps(meta[list]) {
			for _, field := range []string{"serviceUri", "httpTriggerUrl", "callbackUrl"} {
				if s, ok := r[field].(string); ok {
					r[field] = stripURLSecrets(s)
				}
			}
		}
	}

	// GCP Compute instance: metadata.items[]{key, value} holds startup scripts and
	// user-data, the GCE equivalent of EC2 UserData.
	if md, ok := meta["metadata"].(map[string]any); ok {
		for _, item := range asMaps(md["items"]) {
			if _, isItem := item["key"]; isItem {
				redactString(item, "value")
			}
		}
	}

	// GCP GKE cluster: node metadata (scripts, kube-env).
	if nc, ok := meta["node_config"].(map[string]any); ok {
		redactAllValues(nc["metadata"])
	}
	for _, pool := range asMaps(meta["node_pools"]) {
		if cfg, ok := pool["config"].(map[string]any); ok {
			redactAllValues(cfg["metadata"])
		}
	}
}

// RedactMetaJSON applies RedactMetaSecrets to a JSON-encoded meta object. Input
// that is not a JSON object is returned unchanged.
func RedactMetaJSON(metaJSON string) string {
	var meta map[string]any
	dec := json.NewDecoder(strings.NewReader(metaJSON))
	dec.UseNumber() // keep integers exact through the round trip
	if err := dec.Decode(&meta); err != nil || meta == nil {
		return metaJSON
	}
	RedactMetaSecrets(meta)
	out, err := json.Marshal(meta)
	if err != nil {
		return metaJSON
	}
	return string(out)
}

// secretKeyNames are leaf keys whose value is a credential wherever they appear,
// compared after lowercasing and dropping '_', '-' and '.'. Exact matches only:
// SecretArn, NextToken and KmsKeyId name or reference a secret without holding it.
var secretKeyNames = map[string]bool{
	"password": true, "passwd": true, "adminpassword": true, "masteruserpassword": true,
	"secret": true, "clientsecret": true, "secretvalue": true, "secretaccesskey": true,
	"token": true, "accesstoken": true, "refreshtoken": true, "authtoken": true,
	"authkey": true, "sharedkey": true, "authorizationkey": true, "clientkey": true, "privatekey": true,
	"connectionstring": true, "apikey": true, "accesskey": true,
}

func normalizeKey(k string) string {
	return strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(k))
}

// redactSecretKeys walks the whole tree and redacts string values under a
// secretKeyNames key, so a newly collected field with an obvious credential name
// is covered without a path rule.
func redactSecretKeys(v any) {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if s, ok := child.(string); ok {
				if s != "" && secretKeyNames[normalizeKey(k)] {
					t[k] = RedactedValue
				}
				continue
			}
			redactSecretKeys(child)
		}
	case map[string]string:
		for k, s := range t {
			if s != "" && secretKeyNames[normalizeKey(k)] {
				t[k] = RedactedValue
			}
		}
	case []any:
		for _, child := range t {
			redactSecretKeys(child)
		}
	case []map[string]any:
		for _, child := range t {
			redactSecretKeys(child)
		}
	}
}

// redactEnvList redacts the value of each {name, value} entry that is not a
// resource reference. Entries without a value (secret references) are left alone.
func redactEnvList(list any, nameKey, valueKey string) {
	for _, e := range asMaps(list) {
		s, ok := e[valueKey].(string)
		if !ok {
			continue
		}
		name, _ := e[nameKey].(string)
		if !isResourceReference(name, s) {
			e[valueKey] = RedactedValue
		}
	}
}

func redactString(m map[string]any, key string) {
	if s, ok := m[key].(string); ok && s != "" {
		m[key] = RedactedValue
	}
}

func redactAllValues(v any) {
	switch m := v.(type) {
	case map[string]any:
		for k, child := range m {
			if s, ok := child.(string); ok && s != "" {
				m[k] = RedactedValue
			}
		}
	case map[string]string:
		for k, s := range m {
			if s != "" {
				m[k] = RedactedValue
			}
		}
	}
}

// asMaps returns the objects in a list, whether it came from JSON ([]any) or was
// built by a collector ([]map[string]any).
func asMaps(v any) []map[string]any {
	if typed, ok := v.([]map[string]any); ok {
		return typed
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// stripURLSecrets drops the query string, fragment and userinfo of a URL, where
// receiver keys and SAS signatures live, and keeps scheme, host and path.
// Anything that does not parse as an absolute URL is redacted outright.
func stripURLSecrets(raw string) string {
	if raw == "" || raw == RedactedValue {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return RedactedValue
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment = nil, "", "", ""
	return u.String()
}

var (
	arnPattern = regexp.MustCompile(`^arn:aws(-[a-z]+)*:[a-z0-9-]+:`)
	// SQS queue URLs: sqs.<region>.amazonaws.com[.cn], dual-stack sqs.<region>.api.aws,
	// the legacy queue.amazonaws.com and <region>.queue.amazonaws.com hosts, and
	// interface VPC endpoints.
	sqsURLPattern = regexp.MustCompile(`^https://(sqs\.[a-z0-9-]+\.amazonaws\.com(\.cn)?|sqs\.[a-z0-9-]+\.api\.aws|queue\.amazonaws\.com|[a-z0-9-]+\.queue\.amazonaws\.com(\.cn)?|vpce-[a-z0-9-]+\.sqs\.[a-z0-9-]+\.vpce\.amazonaws\.com)/[0-9]{12}/[A-Za-z0-9_-]{1,80}(\.fifo)?$`)
	// DynamoDB table names: 3-255 of [A-Za-z0-9_.-].
	dynamoTableNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,255}$`)
)

// isResourceReference reports whether an environment variable's value names
// another resource rather than holding a secret. The knowledge graph reads these
// (an ARN anywhere, QUEUE_URL, TOPIC_ARN, DYNAMODB_TABLE).
func isResourceReference(name, value string) bool {
	if arnPattern.MatchString(value) || sqsURLPattern.MatchString(value) {
		return true
	}
	return strings.EqualFold(name, "DYNAMODB_TABLE") && dynamoTableNamePattern.MatchString(value)
}
