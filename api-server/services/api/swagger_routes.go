package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/swaggo/swag"
)

// mergedOpenAPIHandler returns the swag-generated spec augmented with stub
// entries for every route registered on `r` that swag didn't already document.
// This guarantees Swagger UI lists 100% of the route table (with rich docs
// on annotated endpoints, plain stubs on the rest).
//
// The merge is computed lazily on first request, then cached. Routes are
// fixed at startup so the cache never invalidates during the process lifetime.
func mergedOpenAPIHandler(r *gin.Engine) gin.HandlerFunc {
	var (
		once   sync.Once
		cached []byte
		merr   error
	)

	return func(c *gin.Context) {
		once.Do(func() {
			cached, merr = buildMergedOpenAPI(r)
		})
		if merr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": merr.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", cached)
	}
}

func buildMergedOpenAPI(r *gin.Engine) ([]byte, error) {
	// Pull the swag-generated swagger.json (registered by docs/swagger/docs.go).
	doc, err := swag.ReadDoc()
	if err != nil {
		return nil, err
	}

	var spec map[string]interface{}
	if err := json.Unmarshal([]byte(doc), &spec); err != nil {
		return nil, err
	}

	paths, _ := spec["paths"].(map[string]interface{})
	if paths == nil {
		paths = map[string]interface{}{}
		spec["paths"] = paths
	}

	for _, route := range r.Routes() {
		swPath := ginPathToSwagger(route.Path)
		method := strings.ToLower(route.Method)

		// Skip Swagger UI's own routes — listing them is noise.
		if strings.HasPrefix(swPath, "/swagger/") || swPath == "/openapi.json" {
			continue
		}
		// Skip pprof — internal debugging surface.
		if strings.HasPrefix(swPath, "/debug/pprof") {
			continue
		}

		methods, ok := paths[swPath].(map[string]interface{})
		if !ok {
			methods = map[string]interface{}{}
			paths[swPath] = methods
		}
		if _, alreadyDocumented := methods[method]; alreadyDocumented {
			continue
		}
		methods[method] = stubOperation(route.Handler, swPath, method)
	}

	// Inject tenant/user header inputs onto every non-public operation so
	// Swagger UI renders editable header fields. The backend reads these
	// directly off c.Request.Header to scope the call (see
	// buildContextFromPayload in api/actions.go and siblings).
	injectScopeHeaders(paths)

	return json.Marshal(spec)
}

// injectScopeHeaders adds `x-tenant-id` and `x-user-id` header parameters to
// every operation that doesn't already declare them. Skips public-bypass
// paths (health, webhooks, swagger).
func injectScopeHeaders(paths map[string]interface{}) {
	scopeHeaders := []map[string]interface{}{
		{
			"name":        "x-tenant-id",
			"in":          "header",
			"required":    false,
			"type":        "string",
			"description": "Scope the call to a tenant. Falls back to `session_variables.tenant_id` if unset.",
		},
		{
			"name":        "x-user-id",
			"in":          "header",
			"required":    false,
			"type":        "string",
			"description": "Scope the call to a user. Falls back to `session_variables.user_id` if unset.",
		},
	}

	for swPath, raw := range paths {
		if isPublicPath(swPath) {
			continue
		}
		methods, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		for _, op := range methods {
			opMap, ok := op.(map[string]interface{})
			if !ok {
				continue
			}
			existing, _ := opMap["parameters"].([]interface{})
			seen := map[string]bool{}
			for _, p := range existing {
				if pm, ok := p.(map[string]interface{}); ok {
					if pm["in"] == "header" {
						if name, _ := pm["name"].(string); name != "" {
							seen[strings.ToLower(name)] = true
						}
					}
				}
			}
			for _, h := range scopeHeaders {
				if seen[strings.ToLower(h["name"].(string))] {
					continue
				}
				existing = append(existing, h)
			}
			opMap["parameters"] = existing
		}
	}
}

func isPublicPath(p string) bool {
	if p == "/health" || p == "/openapi.json" {
		return true
	}
	return strings.HasPrefix(p, "/api/webhooks/") || strings.HasPrefix(p, "/swagger/")
}

// ginParamRe matches gin's `:param` and `*param` placeholders.
var ginParamRe = regexp.MustCompile(`[:*]([A-Za-z0-9_]+)`)

// actionEnvelopeSchema returns an inline JSON-schema describing the standard
// action request envelope, with an `example` payload Swagger UI uses to
// pre-fill the "Try it out" body editor. Each property carries its own
// example so the editor surfaces realistic placeholders.
func actionEnvelopeSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Action name being invoked (matches the dispatcher's switch statement).",
						"example":     "ai_get_tools",
					},
				},
				"required": []string{"name"},
			},
			"input": map[string]interface{}{
				"type":                 "object",
				"description":          "Action-specific input fields. Shape depends on the action; see api-server/migrations/metadata/actions.yaml.",
				"additionalProperties": true,
				"example": map[string]interface{}{
					"request": map[string]interface{}{},
				},
			},
			"session_variables": map[string]interface{}{
				"type":        "object",
				"description": "Session vars (snake_case). Use `role: admin` (with empty tenant/user) for super-admin; set tenant/user IDs to scope the call.",
				"properties": map[string]interface{}{
					"role":          map[string]interface{}{"type": "string", "example": "admin"},
					"tenant_id":     map[string]interface{}{"type": "string", "example": ""},
					"user_id":       map[string]interface{}{"type": "string", "example": ""},
					"allowed_roles": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "example": []string{"admin"}},
				},
				"additionalProperties": map[string]interface{}{"type": "string"},
			},
			"request_query": map[string]interface{}{
				"type":        "string",
				"description": "Optional GraphQL query that triggered the action. Usually omitted.",
				"example":     "",
			},
		},
		"example": map[string]interface{}{
			"action": map[string]interface{}{
				"name": "<action_name>",
			},
			"input": map[string]interface{}{
				"request": map[string]interface{}{},
			},
			"session_variables": map[string]interface{}{
				"role":          "admin",
				"tenant_id":     "",
				"user_id":       "",
				"allowed_roles": []string{"admin"},
			},
		},
	}
}

// ginPathToSwagger converts gin path style (`/v2/:tenant_id/*action`) to
// OpenAPI 2.0 style (`/v2/{tenant_id}/{action}`).
func ginPathToSwagger(p string) string {
	return ginParamRe.ReplaceAllString(p, "{$1}")
}

// funcSuffixRe matches Go's auto-generated closure suffixes like `.func25`,
// `.func1.2`, `.funcN.M.K` etc.
var funcSuffixRe = regexp.MustCompile(`(\.func\d+)+(\.\d+)*$`)

// cleanHandlerName strips the module path and Go's auto-generated `.funcN`
// closure suffixes so that a route handler reported by gin as
// `nudgebee/services/api.handleApis.func25` reads as `handleApis`.
// If only `funcN` remains (top-level closure with no enclosing func), returns
// the original name to avoid losing all signal.
func cleanHandlerName(name string) string {
	short := name
	if idx := strings.LastIndex(short, "/"); idx != -1 {
		short = short[idx+1:]
	}
	if dot := strings.Index(short, "."); dot != -1 {
		short = short[dot+1:]
	}
	stripped := funcSuffixRe.ReplaceAllString(short, "")
	if stripped == "" || strings.HasPrefix(stripped, "func") {
		return short
	}
	return stripped
}

// summaryFromPath derives a human-readable operation summary from the route
// path's last segment, e.g. "/entitlement/check-incident" → "Check Incident",
// "/relay/{action}" → "Relay". Falls back to the full path if the last
// segment is a parameter placeholder.
func summaryFromPath(p string) string {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	if len(segs) == 0 {
		return p
	}
	last := segs[len(segs)-1]
	if strings.HasPrefix(last, "{") {
		// Trailing placeholder — use the previous segment if available.
		if len(segs) >= 2 {
			last = segs[len(segs)-2]
		} else {
			return p
		}
	}
	last = strings.ReplaceAll(last, "-", " ")
	last = strings.ReplaceAll(last, "_", " ")
	parts := strings.Fields(last)
	for i, w := range parts {
		if w == "" {
			continue
		}
		parts[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(parts, " ")
}

// stubOperation builds a minimal Operation object for a route swag didn't
// document. The "internal" tag groups all such routes together in Swagger UI.
// For methods that conventionally accept a body, a free-form JSON body
// parameter is added so the "Try it out" panel renders an editor.
func stubOperation(handlerName, path, method string) map[string]interface{} {
	op := map[string]interface{}{
		"tags":        []string{"internal"},
		"summary":     summaryFromPath(path),
		"description": "Registered inside `" + cleanHandlerName(handlerName) + "`.",
		"consumes":    []string{"application/json"},
		"produces":    []string{"application/json"},
		"security": []map[string]interface{}{
			{"ActionToken": []string{}},
		},
		"responses": map[string]interface{}{
			"default": map[string]interface{}{
				"description": "Response shape depends on the handler.",
			},
		},
	}

	// Use []interface{} so injectScopeHeaders' type assertion succeeds when it
	// appends additional headers downstream.
	params := []interface{}{}

	// Surface path parameters declared in the route (gin :param / *param).
	matches := ginParamRe.FindAllStringSubmatch(path, -1)
	seen := map[string]bool{}
	for _, m := range matches {
		name := m[1]
		if seen[name] {
			continue
		}
		seen[name] = true
		params = append(params, map[string]interface{}{
			"name":     name,
			"in":       "path",
			"required": true,
			"type":     "string",
		})
	}

	// Body editor for write methods. Pre-fill with the action envelope
	// — most internal handlers consume that shape. Override per-route by
	// adding a typed annotation in api/swagger_annotations.go.
	switch method {
	case "post", "put", "patch", "delete":
		params = append(params, map[string]interface{}{
			"name":        "body",
			"in":          "body",
			"required":    false,
			"description": "Action envelope. Replace `<action_name>`, fill `input` with the action's expected fields, and set `session_variables` to scope the call (use `role: admin` for super-admin, or set tenant/user IDs to scope to a tenant).",
			"schema":      actionEnvelopeSchema(),
		})
	}

	if len(params) > 0 {
		op["parameters"] = params
	}

	return op
}
