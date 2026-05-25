package agents

import (
	"fmt"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

var logAnalysisData1 = `@loganalysis analyse the following log and provide the root cause and possible actions to resolve the issue \n {"pattern":{"cluster":"cluster-name","container":"node-agent","container_id":"/k8s/nudgebee/auto-pilot-server-545685ddf4-wg79j/auto-pilot-server","endpoint":"http","instance":"10.42.202.4:80","job":"nudgebee-agent/nudgebee-node-agent","level":"error","machine_id":"9ef20ef2abb073f2a4e2585e66f3e34e","namespace":"nudgebee-agent","pattern_hash":"07319c07f7f422270dd6fba55991d8eb","prometheus":"victoria/victoria-victoria-metrics-k8s-stack","sample":"{\"asctime\": \"2025-04-24 14:46:05,174\", \"levelname\": \"ERROR\", \"filename\": \"database.py\", \"lineno\": 124, \"message\": \"Error executing query: \\n                    select\\n                        ap.id,\\n                        ap.name,\\n                        ap.last_executed_time,\\n                        ap.status,\\n                        ap.created_at,\\n                        u.username as created_by\\n                    from\\n                        auto_playbook ap\\n                    join users u on\\n                        ap.created_by = u.id\\n                    where\\n                        resource_filter @> '[{\\\"type\\\":\\\"Deployment\\\",\\\"namespace\\\":\\\"nudgebee\\\",\\\"name\\\":\\\"auto-pilot-server\\\"}]'\\n                        and account_id = 'undefined'\\n                        and tenant_id = '890cad87-c452-4aa7-b84a-742cee0454a1'\\n                , Error: invalid input syntax for type uuid: \\\"undefined\\\"\\nLINE 15:                         and account_id = 'undefined'\\n                                                  ^\\n\", \"exc_info\": \"Traceback (most recent call last):\\n  File \\\"/app/util/database.py\\\", line 113, in run_query\\n    cur.execute(query)\\n  File \\\"/opt/pysetup/.venv/lib/python3.12/site-packages/psycopg2/extras.py\\\", line 236, in execute\\n    return super().execute(query, vars)\\n           ^^^^^^^^^^^^^^^^^^^^^^^^^^^^\\npsycopg2.errors.InvalidTextRepresentation: invalid input syntax for type uuid: \\\"undefined\\\"\\nLINE 15:                         and account_id = 'undefined'\\n                                                  ^\\n\", \"taskName\": null}","source":"stdout/stderr","system_uuid":"c1285eab-0505-5f84-8e24-a7f56e53c86e"},"logsStreams":[{"timestamp":"1745506258445090851","log":"{\"asctime\": \"2025-04-24 14:50:58,444\", \"levelname\": \"INFO\", \"filename\": \"health.py\", \"lineno\": 11, \"message\": \"<Response 16 bytes [200 OK]>\", \"taskName\": null}","stream":{"app":"auto-pilot-server","container":"auto-pilot-server","filename":"/var/log/pods/nudgebee_auto-pilot-server-545685ddf4-wg79j_8223ecc3-6d1d-495b-9ca2-e42a30b83554/auto-pilot-server/0.log","instance":"auto-pilot","job":"nudgebee/auto-pilot-server","namespace":"nudgebee","node_name":"k3s-nudgebee-dev-e606-2246bc-node-pool-4196-o26ci","pod":"auto-pilot-server-545685ddf4-wg79j","stream":"stdout"}},{"timestamp":"1745506248444615625","log":"{\"asctime\": \"2025-04-24 14:50:48,443\", \"levelname\": \"INFO\", \"filename\": \"health.py\", \"lineno\": 11, \"message\": \"<Response 16 bytes [200 OK]>\", \"taskName\": null}" `

// TODO mock DBs
// TODO mock Tool Execution
func TestLogAnalysis_Execute(t *testing.T) {
	logAnalysis := LogAnalysisAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-loganalysis-followup-7",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     logAnalysisData3,
			},
			{
				SessionId: "ut-loganalysis-followup-6",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     logAnalysisData4,
			},
			{
				SessionId: "ut-loki-chain-1",
				AccountId: os.Getenv("TEST_LOKICHAIN_ACCOUNT"),
				UserId:    os.Getenv("TEST_LOKICHAIN_USER"),
				Query:     logAnalysisData1,
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, logAnalysis, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		fmt.Println(resp)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logAnalysis.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}

}

const logAnalysisData4 = `
@loganalysis analyse the following log and provide the root cause and possible actions to resolve the issue {"pattern":{"cluster":"cluster-name","container":"node-agent","container_id":"/k8s/nudgebee-on-prem-test/auto-pilot-worker-79b57db7f-vf5ml/auto-pilot-worker","endpoint":"http","instance":"10.42.157.144:80","job":"nudgebee-agent/nudgebee-node-agent","level":"error","machine_id":"9ef20ef2abb073f2a4e2585e66f3e34e","namespace":"nudgebee-agent","pattern_hash":"2c78e844ce8c46f29b644c3e69e51492","pod":"nudgebee-agent-jht2n","prometheus":"victoria/victoria-victoria-metrics-k8s-stack","sample":"{\"asctime\": \"2025-05-07 06:30:07,428\", \"levelname\": \"ERROR\", \"filename\": \"relay_server.py\", \"lineno\": 118, \"message\": \"relay response {'action': 'response', 'request_id': 'deed1fe0-f6b0-470b-9dee-4b1bde3e3487', 'status_code': 500, 'data': {'success': False, 'data': None, 'request_id': 'deed1fe0-f6b0-470b-9dee-4b1bde3e3487'}, 'output_type': 'actions'} for request {'account_id': UUID('2b2a8778-5dc8-4257-ab38-b27bab449ff6'), 'action_name': 'continuous_rightsizing', 'action_params': {'applications': [{'name': 'auto-pilot-worker', 'namespace': 'nudgebee', 'kind': 'Deployment'}], 'setting': {'default_min_cpu': 0.01, 'default_min_memory': 100, 'oom_kill_increase_factor': 1.4, 'change_threshold': 10, 'cpu_analysis_percentile': 90, 'memory_analysis_percentile': 90, 'default_analysis_duration_hour': 168, 'recommend_only': False, 'identifier': '70d16cd3-b5f4-4288-a8f8-b7dd9f0c83cf/d3b7a2cd-4bc6-498c-8d4e-3d9e200abc95'}}}\", \"exc_info\": \"NoneType: None\", \"taskName\": null}","source":"stdout/stderr","system_uuid":"2d7a3b8e-0902-55bb-91f5-c4203c215c41"},"logsStreams":{"timestamp":"1746599707354977155","log":"{\"asctime\": \"2025-05-07 06:35:07,354\", \"levelname\": \"INFO\", \"filename\": \"database.py\", \"lineno\": 193, \"message\": \"Inserted data into auto_playbook table\", \"taskName\": null}","stream":{"app":"auto-pilot-worker","container":"auto-pilot-worker","filename":"/var/log/pods/nudgebee-on-prem-test_auto-pilot-worker-79b57db7f-vf5ml_94329ad9-c287-4f96-9a8e-1d4952e40b2d/auto-pilot-worker/0.log","instance":"nudgebee","job":"nudgebee-on-prem-test/auto-pilot-worker","namespace":"nudgebee-on-prem-test","node_name":"k3s-nudgebee-dev-e606-2246bc-node-pool-4196-e2avu","pod":"auto-pilot-worker-79b57db7f-vf5ml","stream":"stdout"}}}`

const logAnalysisData3 = `
@loganalysis analyse the following log and provide the root cause and possible actions to resolve the issue {"app":"ticket-server","container":"ticket-server","filename":"/var/log/pods/nudgebee_ticket-server-55c7c6f4b-d24l7_56bb4cc8-f078-422e-a1c1-e626495dd5e8/ticket-server/0.log","instance":"ticket-server","job":"nudgebee/ticket-server","namespace":"nudgebee","node_name":"k3s-nudgebee-dev-e606-2246bc-node-pool-4196-6w59o","pod":"ticket-server-55c7c6f4b-d24l7","stream":"stdout"} message:{"time":"2025-05-06T11:30:07.520841086Z","level":"INFO","msg":"Error fetching Jira issue details:","error":{"message":"Issue does not exist or you do not have permission to see it.: request failed. Please analyze the request body for more details. Status code: 404","type":"*jira.Error","stacktrace":"nudgebee/tickets-server/services/tools.FetchFullIssueDetails(0xc0003be5c0?, {0xc00086a188?, 0xc0003c4380?})\n\t/app/services/tools/jira_service.go:183 +0xfb\nnudgebee/tickets-server/services.SyncTickets()\n\t/app/services/sync_service.go:140 +0x2f7\ncreated by nudgebee/tickets-server/controllers.SyncTickets in goroutine 283687\n\t/app/controllers/controller.go:114 +0x25\n"}}`
const logAnalysisData2 = `
analyse the following log and provide the root cause and possible actions to resolve the issue 

{"app":"services-server","container":"services-server","filename":"/var/log/pods/nudgebee-test_services-server-785b56fb4f-fcvpf_0b80f712-13e6-4f6f-81b4-73db23d1e09b/services-server/0.log","instance":"services-server","job":"nudgebee-test/services-server","namespace":"nudgebee-test","node_name":"k3s-nudgebee-dev-e606-2246bc-node-pool-4196-taatv","pod":"services-server-785b56fb4f-fcvpf","stream":"stdout"} message:{"time":"2025-04-29T09:18:00.28619077Z","level":"ERROR","msg":"anomaly: unable to generate anomaly","cron_job":"Anomaly Execute","cron_id":"18cee9ef-c02e-4c9b-b0c3-9fa01ae49508","trace_id":"00000000000000000000000000000000","error":{"message":"Post \"http://ml-k8s-server:9999/anomaly\": dial tcp 10.43.69.189:9999: connect: connection refused","type":"*url.Error","stacktrace":"nudgebee/services/anomoly.processConfigUsingMlServer.func2({{0x31, 0x15, 0x2, 0x23, 0xb2, 0x7f, 0x54, 0xfe, 0x85, 0x67, ...}, ...})\n\t/app/anomoly/service.go:190 +0x43d\nnudgebee/services/anomoly.processConfigUsingMlServer.func3()\n\t/app/anomoly/service.go:244 +0x1b5\nnudgebee/services/common.(*WorkerPool).safeExecute(0xc0003bcb80?, 0x1a93ca0?, 0x2626cc0?)\n\t/app/common/worker.go:56 +0x47\nnudgebee/services/common.(*WorkerPool).start.func1(0x2)\n\t/app/common/worker.go:39 +0x173\ncreated by nudgebee/services/common.(*WorkerPool).start in goroutine 117904\n\t/app/common/worker.go:35 +0x3e\n"},"account":"5d002081-1a78-48c4-ba04-d84be3a72516","tenant":"d0c69cc8-9f3c-4ea3-ac9b-8422edb8507e","namespace":"nudgebee-on-prem-test","deployment":"app","type":"ErrorRate"}
`

func TestLogAnalysis_WithfollowupForPlanner(t *testing.T) {
	logAnalysis := LogAnalysisAgent{}
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
			AccountId string
			UserId    string
		}{
			{
				SessionId: "ut-loganalysis-followup-5",
				AccountId: os.Getenv("TEST_ACCOUNT"),
				UserId:    os.Getenv("TEST_USER"),
				Query:     logAnalysisData2,
			},
		}
	for _, tc := range testCases {

		err := core.DeleteConversationBySession(tc.SessionId, tc.AccountId, tc.UserId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, logAnalysis, tc.UserId, tc.AccountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, logAnalysis.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)

		debuggerAgent := newK8sDebugAgent(tc.AccountId)
		resp, err = core.HandleConversationSessionRequest(sc, debuggerAgent, tc.UserId, tc.AccountId, tc.SessionId, "Can you investigate based on previous suggestions?")
		assert.Nil(t, err)
		assert.NotNil(t, resp)
	}

}
