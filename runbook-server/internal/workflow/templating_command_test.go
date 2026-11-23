package workflow

import "testing"

func TestRenderCommandQuotesInputs(t *testing.T) {
	cases := []struct{ node, want string }{
		{"node-1", "kubectl cordon node-1 --timeout=5m"},
		{"node-1 ; delete namespace payments", "kubectl cordon 'node-1 ; delete namespace payments' --timeout=5m"},
		{"a && b || c", "kubectl cordon 'a && b || c' --timeout=5m"},
		{"it's", `kubectl cordon 'it'"'"'s' --timeout=5m`},
		{"", "kubectl cordon '' --timeout=5m"},
	}
	for _, engine := range []string{"jinja", "go"} {
		tpl := "kubectl cordon {{ Inputs.node }} --timeout=5m"
		if engine == "go" {
			tpl = "kubectl cordon {{ .Inputs.node }} --timeout=5m"
		}
		for _, c := range cases {
			ctx := NewTemplateContextWithEngine(nil, map[string]any{"node": c.node}, engine)
			got, err := ctx.RenderCommand(tpl)
			if err != nil || got != c.want {
				t.Errorf("%s %q: got %q, %v; want %q", engine, c.node, got, err, c.want)
			}
		}
		ctx := NewTemplateContextWithEngine(nil, map[string]any{"node": ";"}, engine)
		if _, err := ctx.RenderCommand(tpl); err == nil {
			t.Errorf("%s: bare operator must be refused", engine)
		}
	}
}

func TestProcessTaskParamsOnlyQuotesCLITasks(t *testing.T) {
	ctx := NewTemplateContext(nil, map[string]any{"v": "a b"})
	got, err := ProcessTaskParams("k8s.cli", map[string]any{"command": "get {{ v }}"}, ctx)
	if err != nil || got.(map[string]any)["command"] != "get 'a b'" {
		t.Fatalf("k8s.cli: %v %v", got, err)
	}
	got, _ = ProcessTaskParams("http.request", map[string]any{"command": "get {{ v }}"}, ctx)
	if got.(map[string]any)["command"] != "get a b" {
		t.Fatalf("other task must render unchanged: %v", got)
	}
}

func TestRenderCommandGoControlFlow(t *testing.T) {
	ctx := NewTemplateContextWithEngine(nil, map[string]any{"ns": "a b", "on": true}, "go")
	got, err := ctx.RenderCommand("get pods {{ if .Inputs.on }}-n {{ .Inputs.ns }}{{ else }}-A{{ end }}{{/* c */}}")
	if err != nil || got != "get pods -n 'a b'" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestRenderCommandMultilineExpression(t *testing.T) {
	ctx := NewTemplateContext(nil, map[string]any{"node": "n ; get ns"})
	got, err := ctx.RenderCommand("cordon {{ Inputs.node\n }}")
	if err != nil || got != "cordon 'n ; get ns'" {
		t.Fatalf("got %q, %v", got, err)
	}
}
