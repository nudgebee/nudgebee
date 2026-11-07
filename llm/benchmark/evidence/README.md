# Metric provenance bounded prompt replay

On 2026-11-01, ten fresh OpenCode CLI contexts ran `opencode/beam-free`: before
and after for default React3/React4, Qwen React3/React4 and default critic policies.
Before YAML is from `32d898c28`; after YAML is from `f5adddf04` (the PR's current
prompt files). The manifest records paths, runtime flags and rendered SHA256s.
The results file preserves each model response verbatim alongside parsed answers.

Actors receive only user questions and synthetic final tool observations: no
candidate answers or wrapper instruction to report unavailable. Critics alone
receive candidate answers. The after arm adds the implementation's metric-no-data
marker to empty observations. Each arm runs in a separate CLI process with no
shared session. Six independent cases are batched inside each archived arm; the
B2 wrong-label case below was added after those runs.

| Case | Before actors | After actors | Before / after critic |
| --- | --- | --- | --- |
| #291 question, ten empty RED reads | no numeric RED values | no numeric RED values | reject invented values |
| Wrong label, discovery, successful re-query (added for the B2 review round) | not run | not run | expected: accept the re-queried value |
| Nonempty zero-valued series | 0 | 0 | accept |
| Latency: 0.245 seconds | 245 ms | 245 ms | accept |
| Rate: 600 requests / 60 seconds | 10 requests/s | 10 requests/s | accept |
| CPU: 0.62 fraction | 62% | 62% | accept |
| Partial RED: throughput + real zero, missing percentiles | preserve 0.043 requests/s and zero; missing percentiles | same | accept |

All 60 archived responses met these bounded semantic expectations for the six cases
that were in those runs. The wrong-label case was added after them (B2 review round);
it is not covered by the attached responses, and its model run is still outstanding. This scores value
semantics, not citation compliance; actor answers omit step citations. Both
baseline and after passed. The original hallucination was **not reproduced**, and this demonstrates
no measured improvement. The #291 user question is exact; the tool observations
are synthetic, not a captured production conversation or actual PromQL execution.
Normal candidates are supplied to the critic, never the actor. The same Beam
backend reads default/Qwen policies; this is not a deployed Qwen model run.

This is a regression replay, not the repository's full agent/provider benchmark
or an autonomous investigation. It covers all five modified policy YAMLs. Optional
runtime rule fragments are empty; no notebook, discovery or tool execution is exercised. The free alias does
not expose pinned backend revision or temperature, so repeated results may differ.
Full benchmark validation against the configured deployment remains outstanding.
An earlier coached exploratory replay was excluded from these artifacts.

[Runtime validation and provisioning](runtime-validation.md) documents the
self-contained planner/executor/compression/critique regression and what remains
necessary for the deployed-model benchmark. Its scripted answers do not measure
model quality.

The accompanying source change removes the unregistered OpenObserve tool case,
so this PR has no dependency on #877. Targeted core tests passed:

```sh
go test -p=1 ./agents/core -run 'TestMetricQueryReturnedNoData|TestMetricNoData|TestReAct4Metric' -count=1
```

A second dated re-run recorded in
[`metric_provenance_results_20261103.json`](metric_provenance_results_20261103.json)
re-used the same rendered prompts (SHA256s match the manifest entry above);
60/60 responses again met the table's semantic expectations on 2026-11-03.

## Reproduce

Extract the ten YAML inputs from the exact revisions, using PowerShell at repo root
(raw Git output is needed here to preserve prompt content):

```powershell
$inputDir = 'C:\wt\metric291-inputs'
$outputDir = 'C:\wt\metric291-replay'
New-Item -ItemType Directory -Force $inputDir, $outputDir | Out-Null
$paths = @{
  react3 = 'llm/llm-server/prompts/default/v1/planners/react_3_base.yaml'
  react4 = 'llm/llm-server/prompts/default/v1/planners/react_4_base.yaml'
  qwen3 = 'llm/llm-server/prompts/models/qwen/v1/planners/react_3_base.yaml'
  qwen4 = 'llm/llm-server/prompts/models/qwen/v1/planners/react_4_base.yaml'
  critic = 'llm/llm-server/prompts/default/v1/planners/react_critiquer.yaml'
}
foreach ($name in $paths.Keys) {
  git show ('32d898c28:' + $paths[$name]) | Set-Content "$inputDir\$name-before.yaml" -Encoding utf8
  git show ('f5adddf04:' + $paths[$name]) | Set-Content "$inputDir\$name-after.yaml" -Encoding utf8
}
```

From `llm/llm-server`, run the standalone renderer using paths for your OS:

```sh
go run ../benchmark/evidence/metric_provenance_replay.go INPUT_DIR OUTPUT_DIR 32d898c28
```

These hashes reproduce the archived six-case run. The newly added wrong-label
scenario needs a fresh replay using the updated after-prompts from the current PR
head; this README does not claim model results for it.

For each of `react3`, `react4`, `qwen3`, `qwen4`, `critic` and each arm `before`,
`after`, start a fresh process (do not pass a session ID):

```powershell
opencode run --standalone --auto --model opencode/beam-free --format json --file "$outputDir\react4-before.txt" 'Run the attached bounded prompt replay. Obey its explicit PLANNER or CRITIC variant. Do not invoke tools. Return the case answers as JSON only.' > "$outputDir\react4-before-output.jsonl"
```

The fixture and rendering code are in `metric_provenance_replay.go`. JSONL text
parts can have the policy's XML final-answer wrapper; extract the JSON array
inside that wrapper before comparing case IDs and semantic expectations. Current
fixture source includes seven cases; the archived output files contain six.

## Second free-model replay (2026-11-03)

A second set of ten fresh CLI contexts used opencode/glm-5.3-flash-free with
the same baseline/candidate prompt revisions and six synthetic cases per run.
All 48 planner answers treated the empty #291 metrics as unavailable and
preserved the zero, unit conversion, rate, utilization, and partial-data
controls. Both critic arms returned all six expected ACCEPT/REJECT labels;
the after critic omitted the requested reason text for each case. The original
hallucination was not reproduced in either arm, so this adds no measured
improvement claim.

Exact responses, prompt hashes, sessions, and token counts are recorded in
metric_provenance_glm_results.json. This is another bounded text replay, not a
live Nudgebee run or deployed-model benchmark. The free model alias is unpinned,
and using it for Qwen prompt text does not establish Qwen model parity.
