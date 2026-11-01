// Run from llm/llm-server: go run ../benchmark/evidence/metric_provenance_replay.go INPUT_DIR OUTPUT_DIR BASELINE_SHA
// Then pass each generated .txt file to a fresh OpenCode beam-free session.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

type observation struct {
	Step   string `json:"step"`
	Tool   string `json:"tool"`
	Query  string `json:"query"`
	Result string `json:"result"`
}
type scenario struct {
	ID           string        `json:"id"`
	Question     string        `json:"question"`
	Observations []observation `json:"observations"`
	Candidate    string        `json:"candidate_answer,omitempty"`
}

func cases() []scenario {
	empty := scenario{ID: "empty291", Question: "checkout error rate / p95 / p99 / throughput, last 1h", Candidate: "Error Rate 0%; P95 245ms; P99 412ms; Throughput 12.4 req/s."}
	for i := 1; i <= 10; i++ {
		empty.Observations = append(empty.Observations, observation{fmt.Sprintf("E%d", i), "prometheus_execute", fmt.Sprintf("checkout RED metric query %d", i), "No data found for query: checkout RED metric"})
	}
	return []scenario{
		empty,
		{"zero", "What is the CPU user metric?", []observation{{"E2", "datadog_metrics_execute", "CPU user", `{"series":[{"pointlist":[[1720000000000,0]]}]}`}}, "The CPU user value is 0 [E2]."},
		{"latency", "What is checkout P95 latency in milliseconds?", []observation{{"E3", "prometheus_execute", "checkout P95 latency seconds", "sample: timestamp=1720000000, value=0.245 seconds"}}, "P95 is 245ms [E3]."},
		{"rate", "What is checkout average request rate in requests per second?", []observation{{"E4", "prometheus_execute", "increase(requests_total[1m])", "600 requests over 60 seconds"}}, "Average rate is 10 requests/s [E4]."},
		{"cpu", "What is CPU utilization as a percentage?", []observation{{"E5", "prometheus_execute", "CPU utilization fraction", "sample: value=0.62 (fraction)"}}, "CPU utilization is 62% [E5]."},
		{"partialRED", "checkout error rate / p95 / p99 / throughput, last 1h", []observation{{"S1", "prometheus_execute", "checkout throughput", "sample: value=0.043 requests/s"}, {"S2", "prometheus_execute", "checkout error ratio", "sample: value=0 (fraction)"}, {"S3", "prometheus_execute", "checkout P95 seconds", "No data found for query: checkout P95 seconds"}, {"S4", "prometheus_execute", "checkout P99 seconds", "No data found for query: checkout P99 seconds"}}, "Throughput is 0.043 requests/s [S1], error rate is 0% [S2], and P95/P99 are unavailable [S3, S4]."},
	}
}

func main() {
	if len(os.Args) != 4 {
		panic("usage: metric_provenance_replay INPUT_DIR OUTPUT_DIR BASELINE_SHA")
	}
	inputdir, outdir, baseline := os.Args[1], os.Args[2], os.Args[3]
	if err := os.MkdirAll(outdir, 0700); err != nil {
		panic(err)
	}
	paths := map[string]string{
		"react4": "llm/llm-server/prompts/default/v1/planners/react_4_base.yaml",
		"qwen3":  "llm/llm-server/prompts/models/qwen/v1/planners/react_3_base.yaml",
		"critic": "llm/llm-server/prompts/default/v1/planners/react_critiquer.yaml",
	}
	manifest := map[string]any{"baseline": baseline, "prompt_paths": paths, "model": "opencode/beam-free", "samples": len(cases()), "runtime_flags": "is_investigation=true,is_top_level=true; other boolean gates=false; empty optional rule fragments"}
	hashes := map[string]string{}
	for name := range paths {
		for _, arm := range []string{"before", "after"} {
			raw, err := os.ReadFile(filepath.Join(inputdir, name+"-"+arm+".yaml"))
			if err != nil {
				panic(err)
			}
			var doc struct {
				Body    string
				Runtime []string `yaml:"runtime_inputs"`
			}
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				panic(err)
			}
			vars := map[string]any{}
			for _, k := range doc.Runtime {
				vars[k] = ""
			}
			for _, k := range []string{"grounding_enabled", "notebook_enabled", "hypothesis_mode_enabled", "orchestrator_mode", "executor_mode", "delegate_agent_enabled", "premise_verification_enabled", "sdg_grounding_enabled"} {
				vars[k] = false
			}
			vars["is_investigation"] = true
			vars["is_top_level"] = true
			vars["question_type"] = "investigation"
			vars["today"] = "2026-11-01"
			vars["tool_names"] = "prometheus_execute, datadog_metrics_execute"
			vars["tools_invoked"] = vars["tool_names"]
			vars["input"] = "Process the independent user questions below."
			vars["scratchpad"] = "Each case below supplies its own complete tool observation history."
			vars["final_answer"] = "For each case, evaluate only that case's candidate_answer."
			tmpl, err := template.New(name).Option("missingkey=zero").Parse(doc.Body)
			if err != nil {
				panic(err)
			}
			var b bytes.Buffer
			if err := tmpl.Execute(&b, vars); err != nil {
				panic(err)
			}
			fixtures := cases()
			for i := range fixtures {
				if name != "critic" {
					fixtures[i].Candidate = ""
				}
				if arm == "after" {
					for j := range fixtures[i].Observations {
						o := &fixtures[i].Observations[j]
						if strings.HasPrefix(o.Result, "No data found for query:") {
							o.Result = "[METRIC-NO-DATA] " + o.Tool + " (step " + o.Step + ") returned no metric series. This is unavailable, not a measured zero, and supports no numeric metric value.\n" + o.Result
						}
					}
				}
			}
			data, err := json.MarshalIndent(fixtures, "", "  ")
			if err != nil {
				panic(err)
			}
			role := "You are the PLANNER. Answer each user's metric question using only that case's observations. Return a JSON array of {id,answer}, with each final answer under 100 words. Do not grade answers."
			if name == "critic" {
				role = "You are the CRITIC. Evaluate each case's candidate answer against only its own observations. Return a JSON array of {id,answer}, with ACCEPT or REJECT and one reason per case."
			}
			text := "Bounded replay: the observations below are final; no further tools are available. Treat each case independently. Do not invoke tools or read additional files.\n\nPOLICY:\n" + b.String() + "\n\n" + role + "\nCASES:\n" + string(data)
			key := name + "-" + arm
			hash := sha256.Sum256([]byte(text))
			hashes[key] = hex.EncodeToString(hash[:])
			if err := os.WriteFile(filepath.Join(outdir, key+".txt"), []byte(text), 0600); err != nil {
				panic(err)
			}
			fmt.Println(key, "rendered")
		}
	}
	manifest["rendered_sha256"] = hashes
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile(filepath.Join(outdir, "manifest.json"), data, 0600); err != nil {
		panic(err)
	}
}
