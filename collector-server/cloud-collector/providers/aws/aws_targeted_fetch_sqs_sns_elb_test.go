package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"nudgebee/collector/cloud/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	fakeAwsRegion  = "us-east-1"
	fakeAwsAccount = "123456789012"
)

// fakeAwsRequest is one AWS API call as the fake endpoint saw it. Op is
// "<service>:<Action>"; Params holds the query-protocol form fields or, for the
// JSON protocol SQS speaks, the top-level string fields of the body.
type fakeAwsRequest struct {
	Op     string
	Params map[string]string
}

// fakeAwsEndpoint stands in for the regional AWS endpoints. Every SDK client the
// provider builds is pointed at it through AWS_ENDPOINT_URL, so these tests run
// the real GetResources / GetResourcesByIds code, SDK deserializers included.
type fakeAwsEndpoint struct {
	mu    sync.Mutex
	calls []fakeAwsRequest
	// respond returns the HTTP status and body for a call. A JSON-protocol error
	// body is {"__type": "<code>"}; a query-protocol one is built by awsQueryError.
	respond func(req fakeAwsRequest) (int, string)
}

func (f *fakeAwsEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	req := fakeAwsRequest{Params: map[string]string{}}
	if target := r.Header.Get("X-Amz-Target"); strings.HasPrefix(target, "AmazonSQS.") {
		req.Op = "sqs:" + strings.TrimPrefix(target, "AmazonSQS.")
		var fields map[string]any
		_ = json.Unmarshal(body, &fields)
		for k, v := range fields {
			if s, ok := v.(string); ok {
				req.Params[k] = s
			}
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
	} else {
		form, _ := url.ParseQuery(string(body))
		service := map[string]string{"2010-03-31": "sns", "2012-06-01": "elb", "2015-12-01": "elbv2"}[form.Get("Version")]
		req.Op = service + ":" + form.Get("Action")
		for k := range form {
			req.Params[k] = form.Get(k)
		}
		w.Header().Set("Content-Type", "text/xml")
	}

	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.mu.Unlock()

	status, respBody := f.respond(req)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(respBody))
}

// ops returns the operations called since the last reset, in order.
func (f *fakeAwsEndpoint) ops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ops := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		ops = append(ops, c.Op)
	}
	return ops
}

func (f *fakeAwsEndpoint) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// startFakeAws points the AWS SDK at a fake endpoint for the duration of the test.
func startFakeAws(t *testing.T, respond func(req fakeAwsRequest) (int, string)) *fakeAwsEndpoint {
	t.Helper()
	fake := &fakeAwsEndpoint{respond: respond}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	emptyFile := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.WriteFile(emptyFile, nil, 0o600))
	for k, v := range map[string]string{
		"AWS_ENDPOINT_URL":            server.URL,
		"AWS_ACCESS_KEY_ID":           "test",
		"AWS_SECRET_ACCESS_KEY":       "test",
		"AWS_SESSION_TOKEN":           "",
		"AWS_PROFILE":                 "",
		"CLOUD_COLLECTOR_AWS_PROFILE": "",
		"AWS_CONFIG_FILE":             emptyFile,
		"AWS_SHARED_CREDENTIALS_FILE": emptyFile,
		"AWS_EC2_METADATA_DISABLED":   "true",
	} {
		t.Setenv(k, v)
	}
	return fake
}

func awsQueryResponse(action, namespace, result string) string {
	return fmt.Sprintf(`<%[1]sResponse xmlns="%[2]s"><%[1]sResult>%[3]s</%[1]sResult><ResponseMetadata><RequestId>req</RequestId></ResponseMetadata></%[1]sResponse>`, action, namespace, result)
}

func awsQueryError(code string) string {
	return fmt.Sprintf(`<ErrorResponse><Error><Type>Sender</Type><Code>%s</Code><Message>%s</Message></Error><RequestId>req</RequestId></ErrorResponse>`, code, code)
}

func findResourceById(t *testing.T, resources []providers.Resource, id string) providers.Resource {
	t.Helper()
	for _, r := range resources {
		if r.Id == id {
			return r
		}
	}
	require.Failf(t, "resource not found", "no resource with id %q in %d resources", id, len(resources))
	return providers.Resource{}
}

func TestSqsQueueNamesForLookupKeys(t *testing.T) {
	names, err := sqsQueueNamesForLookupKeys([]string{"orders", "arn:aws:sqs:us-east-1:123456789012:orders", "jobs.fifo"})
	require.NoError(t, err)
	assert.Equal(t, []string{"orders", "jobs.fifo"}, names)

	// Shapes the region scan's filter may still match must fall back to it.
	for _, key := range []string{
		"https://sqs.us-east-1.amazonaws.com/123456789012/orders",
		"arn:aws:sns:us-east-1:123456789012:orders",
		"us-east-1:123456789012:orders",
		strings.Repeat("a", 81),
		"",
	} {
		_, err := sqsQueueNamesForLookupKeys([]string{key})
		assert.ErrorIs(t, err, errors.ErrUnsupported, "key %q", key)
	}
}

func TestSnsTopicArnsForLookupKeys(t *testing.T) {
	arns, err := snsTopicArnsForLookupKeys([]string{
		"alerts",
		"arn:aws:sns:us-east-1:123456789012:alerts",
		"arn:aws:sns:eu-west-1:123456789012:alerts", // another region: nothing to fetch here
		"jobs.fifo",
	}, fakeAwsRegion, fakeAwsAccount)
	require.NoError(t, err)
	assert.Equal(t, []string{
		"arn:aws:sns:us-east-1:123456789012:alerts",
		"arn:aws:sns:us-east-1:123456789012:jobs.fifo",
	}, arns)

	for _, key := range []string{
		"arn:aws:sns:us-east-1:123456789012:alerts:6a1f0c0e-subscription",
		"arn:aws:sqs:us-east-1:123456789012:alerts",
		"alerts/critical",
		"",
	} {
		_, err := snsTopicArnsForLookupKeys([]string{key}, fakeAwsRegion, fakeAwsAccount)
		assert.ErrorIs(t, err, errors.ErrUnsupported, "key %q", key)
	}
}

func TestElbLookupTargetsForKeys(t *testing.T) {
	albArn := "arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/web/50dc6c495c0c9188"

	// A bare name is a Classic Id and an ALB/NLB Name: look up both kinds.
	targets, err := elbLookupTargetsForKeys([]string{"web"}, fakeAwsRegion)
	require.NoError(t, err)
	assert.Equal(t, elbLookupTargets{classicNames: []string{"web"}, v2Names: []string{"web"}}, targets)

	targets, err = elbLookupTargetsForKeys([]string{albArn}, fakeAwsRegion)
	require.NoError(t, err)
	assert.Equal(t, elbLookupTargets{v2Arns: []string{albArn}}, targets)

	// The ARN aws_elb.go builds for a Classic Load Balancer names only the Classic one.
	targets, err = elbLookupTargetsForKeys([]string{"arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/web"}, fakeAwsRegion)
	require.NoError(t, err)
	assert.Equal(t, elbLookupTargets{classicNames: []string{"web"}}, targets)

	// Another region: nothing to fetch here.
	targets, err = elbLookupTargetsForKeys([]string{"arn:aws:elasticloadbalancing:eu-west-1:123456789012:loadbalancer/app/web/50dc6c495c0c9188"}, fakeAwsRegion)
	require.NoError(t, err)
	assert.Equal(t, elbLookupTargets{}, targets)

	for _, key := range []string{
		"app/web/50dc6c495c0c9188", // CloudWatch LoadBalancer dimension
		"arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/web/73e2d6bc24d8a067",
		"arn:aws:elasticloadbalancing:us-east-1:123456789012:listener/app/web/50dc6c495c0c9188/f2f7dc8efc522ab2",
		"arn:aws:sqs:us-east-1:123456789012:web",
		"",
	} {
		_, err := elbLookupTargetsForKeys([]string{key}, fakeAwsRegion)
		assert.ErrorIs(t, err, errors.ErrUnsupported, "key %q", key)
	}
}

// A queue lookup must not list the region, must build the row the bulk sync
// builds, and must tell a deleted queue from a failed lookup.
func TestSqsGetResourcesByIds(t *testing.T) {
	queues := map[string]bool{"orders": true, "orders-dlq": true}
	denyGetQueueUrl := false
	attributesGone := false // GetQueueUrl still resolves a queue whose deletion is in progress
	queueName := func(queueURL string) string { return queueURL[strings.LastIndex(queueURL, "/")+1:] }
	fake := startFakeAws(t, func(req fakeAwsRequest) (int, string) {
		switch req.Op {
		case "sqs:ListQueues":
			urls := []string{}
			for _, name := range []string{"orders", "orders-dlq"} {
				if queues[name] {
					urls = append(urls, fmt.Sprintf("https://sqs.us-east-1.amazonaws.com/%s/%s", fakeAwsAccount, name))
				}
			}
			body, _ := json.Marshal(map[string]any{"QueueUrls": urls})
			return 200, string(body)
		case "sqs:GetQueueUrl":
			if denyGetQueueUrl {
				return 403, `{"__type":"com.amazonaws.sqs#AccessDeniedException","message":"denied"}`
			}
			if !queues[req.Params["QueueName"]] {
				return 400, `{"__type":"com.amazonaws.sqs#QueueDoesNotExist","message":"The specified queue does not exist."}`
			}
			return 200, fmt.Sprintf(`{"QueueUrl":"https://sqs.us-east-1.amazonaws.com/%s/%s"}`, fakeAwsAccount, req.Params["QueueName"])
		case "sqs:GetQueueAttributes":
			if attributesGone {
				return 400, `{"__type":"com.amazonaws.sqs#QueueDoesNotExist","message":"The specified queue does not exist."}`
			}
			return 200, fmt.Sprintf(`{"Attributes":{"QueueArn":"arn:aws:sqs:us-east-1:%s:%s","CreatedTimestamp":"1700000000"}}`, fakeAwsAccount, queueName(req.Params["QueueUrl"]))
		case "sqs:ListQueueTags":
			return 200, `{"Tags":{"team":"payments"}}`
		}
		return 500, `{"__type":"UnexpectedCall"}`
	})

	ctx := &targetedFetchTestCtx{ctx: context.Background()}
	account := providers.Account{AccountNumber: fakeAwsAccount}
	svc := &awsQueueService{}

	bulk, err := svc.GetResources(ctx, account, fakeAwsRegion)
	require.NoError(t, err)
	require.Len(t, bulk, 2)
	fake.reset()

	// "orders" is a substring of "orders-dlq"; only the queue asked for comes back.
	got, err := svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{"orders"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, findResourceById(t, bulk, "orders"), got[0], "targeted fetch must build the bulk sync's row")
	assert.NotContains(t, fake.ops(), "sqs:ListQueues", "a lookup by id must not list the region")
	// Both paths share the builder, so pin the row itself too.
	assert.Equal(t, "orders", got[0].Name)
	assert.Equal(t, "arn:aws:sqs:us-east-1:123456789012:orders", got[0].Arn)
	assert.Equal(t, getAwsServiceResourceType(ServiceNameSQS, "queue"), got[0].Type)
	assert.Equal(t, map[string][]string{"team": {"payments"}}, got[0].Tags)
	assert.Equal(t, time.Unix(1700000000, 0), got[0].CreatedAt)

	got, err = svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{"arn:aws:sqs:us-east-1:123456789012:orders"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "orders", got[0].Id)

	// An ARN for another account's queue of the same name is not this queue.
	got, err = svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{"arn:aws:sqs:us-east-1:999999999999:orders"})
	require.NoError(t, err)
	assert.Empty(t, got)

	// DeleteQueue: the queue is gone. Empty and no error, never the DLQ.
	queues["orders"] = false
	fake.reset()
	got, err = svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{"orders"})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, []string{"sqs:GetQueueUrl"}, fake.ops())

	// Just after DeleteQueue, GetQueueUrl can still resolve the queue while its
	// attributes are already gone. That is "gone" too, not a failed lookup.
	attributesGone = true
	got, err = svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{"orders-dlq"})
	require.NoError(t, err)
	assert.Empty(t, got)
	attributesGone = false

	// A denied lookup is not "gone": it must surface as an error (ListResources
	// then falls back to the full scan), and not as ErrUnsupported.
	denyGetQueueUrl = true
	_, err = svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{"orders-dlq"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, errors.ErrUnsupported)
}

func TestSnsGetResourcesByIds(t *testing.T) {
	const ns = "http://sns.amazonaws.com/doc/2010-03-31/"
	topicArn := func(name string) string { return fmt.Sprintf("arn:aws:sns:us-east-1:%s:%s", fakeAwsAccount, name) }
	topics := map[string]bool{topicArn("alerts"): true, topicArn("alerts-critical"): true}
	denyGetTopicAttributes := false
	fake := startFakeAws(t, func(req fakeAwsRequest) (int, string) {
		switch req.Op {
		case "sns:ListTopics":
			members := ""
			for _, name := range []string{"alerts", "alerts-critical"} {
				if topics[topicArn(name)] {
					members += "<member><TopicArn>" + topicArn(name) + "</TopicArn></member>"
				}
			}
			return 200, awsQueryResponse("ListTopics", ns, "<Topics>"+members+"</Topics>")
		case "sns:GetTopicAttributes":
			if denyGetTopicAttributes {
				return 403, awsQueryError("AuthorizationError")
			}
			if !topics[req.Params["TopicArn"]] {
				return 404, awsQueryError("NotFound")
			}
			return 200, awsQueryResponse("GetTopicAttributes", ns,
				"<Attributes><entry><key>TopicArn</key><value>"+req.Params["TopicArn"]+"</value></entry><entry><key>DisplayName</key><value>Alerts</value></entry></Attributes>")
		case "sns:ListTagsForResource":
			return 200, awsQueryResponse("ListTagsForResource", ns, "<Tags><member><Key>team</Key><Value>sre</Value></member></Tags>")
		case "sns:ListSubscriptionsByTopic":
			return 200, awsQueryResponse("ListSubscriptionsByTopic", ns,
				"<Subscriptions><member><TopicArn>"+req.Params["TopicArn"]+"</TopicArn><Protocol>email</Protocol><SubscriptionArn>PendingConfirmation</SubscriptionArn></member></Subscriptions>")
		}
		return 500, awsQueryError("UnexpectedCall")
	})

	ctx := &targetedFetchTestCtx{ctx: context.Background()}
	account := providers.Account{AccountNumber: fakeAwsAccount}
	svc := &amazonSns{}
	// CreatedAt is time.Now() on both paths; it is not part of the row's identity.
	withoutCreatedAt := func(r providers.Resource) providers.Resource {
		r.CreatedAt = time.Time{}
		return r
	}

	bulk, err := svc.GetResources(ctx, account, fakeAwsRegion)
	require.NoError(t, err)
	require.Len(t, bulk, 2)
	fake.reset()

	// "alerts" is a substring of "alerts-critical"; only the topic asked for comes back.
	got, err := svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{"alerts"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, withoutCreatedAt(findResourceById(t, bulk, "alerts")), withoutCreatedAt(got[0]), "targeted fetch must build the bulk sync's row")
	assert.NotContains(t, fake.ops(), "sns:ListTopics", "a lookup by id must not list the region")

	got, err = svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{topicArn("alerts-critical")})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "alerts-critical", got[0].Id)

	// DeleteTopic: the topic is gone. Empty and no error, and one API call.
	topics[topicArn("alerts")] = false
	fake.reset()
	got, err = svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{"alerts"})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, []string{"sns:GetTopicAttributes"}, fake.ops())

	// A denied lookup is not "gone".
	denyGetTopicAttributes = true
	_, err = svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{"alerts-critical"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, errors.ErrUnsupported)
}

func TestElbGetResourcesByIds(t *testing.T) {
	const (
		classicNs = "http://elasticloadbalancing.amazonaws.com/doc/2012-06-01/"
		v2Ns      = "http://elasticloadbalancing.amazonaws.com/doc/2015-12-01/"
	)
	albArn := func(name string) string {
		return fmt.Sprintf("arn:aws:elasticloadbalancing:us-east-1:%s:loadbalancer/app/%s/50dc6c495c0c9188", fakeAwsAccount, name)
	}
	// A Classic Load Balancer and an Application Load Balancer share the name "web".
	classic := map[string]bool{"web": true, "web-2": true}
	albs := []string{"web", "api"}
	denyDescribe := false

	classicMember := func(name string) string {
		return "<member><LoadBalancerName>" + name + "</LoadBalancerName><DNSName>" + name + ".elb.example.com</DNSName></member>"
	}
	albMember := func(name string) string {
		return "<member><LoadBalancerArn>" + albArn(name) + "</LoadBalancerArn><LoadBalancerName>" + name + "</LoadBalancerName><Type>application</Type></member>"
	}
	fake := startFakeAws(t, func(req fakeAwsRequest) (int, string) {
		if denyDescribe {
			return 403, awsQueryError("AccessDenied")
		}
		switch req.Op {
		case "elb:DescribeLoadBalancers":
			members := ""
			if name, filtered := req.Params["LoadBalancerNames.member.1"]; filtered {
				if !classic[name] {
					return 400, awsQueryError("LoadBalancerNotFound")
				}
				members = classicMember(name)
			} else {
				for _, name := range []string{"web", "web-2"} {
					if classic[name] {
						members += classicMember(name)
					}
				}
			}
			return 200, awsQueryResponse("DescribeLoadBalancers", classicNs, "<LoadBalancerDescriptions>"+members+"</LoadBalancerDescriptions>")
		case "elb:DescribeTags":
			name := req.Params["LoadBalancerNames.member.1"]
			return 200, awsQueryResponse("DescribeTags", classicNs,
				"<TagDescriptions><member><LoadBalancerName>"+name+"</LoadBalancerName><Tags><member><Key>kind</Key><Value>classic</Value></member></Tags></member></TagDescriptions>")
		case "elb:DescribeLoadBalancerAttributes":
			return 200, awsQueryResponse("DescribeLoadBalancerAttributes", classicNs,
				"<LoadBalancerAttributes><CrossZoneLoadBalancing><Enabled>true</Enabled></CrossZoneLoadBalancing></LoadBalancerAttributes>")
		case "elbv2:DescribeLoadBalancers":
			wantName, byName := req.Params["Names.member.1"]
			wantArn, byArn := req.Params["LoadBalancerArns.member.1"]
			members := ""
			for _, name := range albs {
				if (!byName && !byArn) || (byName && name == wantName) || (byArn && albArn(name) == wantArn) {
					members += albMember(name)
				}
			}
			if members == "" && (byName || byArn) {
				return 400, awsQueryError("LoadBalancerNotFound")
			}
			return 200, awsQueryResponse("DescribeLoadBalancers", v2Ns, "<LoadBalancers>"+members+"</LoadBalancers>")
		case "elbv2:DescribeTags":
			members := ""
			for k, v := range req.Params {
				if strings.HasPrefix(k, "ResourceArns.member.") {
					members += "<member><ResourceArn>" + v + "</ResourceArn><Tags><member><Key>kind</Key><Value>alb</Value></member></Tags></member>"
				}
			}
			return 200, awsQueryResponse("DescribeTags", v2Ns, "<TagDescriptions>"+members+"</TagDescriptions>")
		case "elbv2:DescribeLoadBalancerAttributes":
			return 200, awsQueryResponse("DescribeLoadBalancerAttributes", v2Ns,
				"<Attributes><member><Key>idle_timeout.timeout_seconds</Key><Value>60</Value></member></Attributes>")
		case "elbv2:DescribeTargetGroups":
			return 200, awsQueryResponse("DescribeTargetGroups", v2Ns, "<TargetGroups/>")
		}
		return 500, awsQueryError("UnexpectedCall")
	})
	// Every DescribeLoadBalancers call of a lookup by id must name what it wants.
	assertNoRegionScan := func() {
		t.Helper()
		fake.mu.Lock()
		defer fake.mu.Unlock()
		for _, call := range fake.calls {
			if !strings.HasSuffix(call.Op, ":DescribeLoadBalancers") {
				continue
			}
			_, classicName := call.Params["LoadBalancerNames.member.1"]
			_, v2Name := call.Params["Names.member.1"]
			_, v2Arn := call.Params["LoadBalancerArns.member.1"]
			assert.True(t, classicName || v2Name || v2Arn, "%s described every load balancer in the region", call.Op)
		}
	}

	ctx := &targetedFetchTestCtx{ctx: context.Background()}
	account := providers.Account{AccountNumber: fakeAwsAccount}
	svc := &awsElb{}

	bulk, err := svc.GetResources(ctx, account, fakeAwsRegion)
	require.NoError(t, err)
	require.Len(t, bulk, 4)
	fake.reset()

	// By ARN: exactly that Application Load Balancer, the bulk sync's row.
	got, err := svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{albArn("api")})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, findResourceById(t, bulk, albArn("api")), got[0], "targeted fetch must build the bulk sync's row")
	assertNoRegionScan()

	// By bare name: the Classic one stored under that Id and the ALB carrying that
	// Name — what the region scan's filter returned — but never "web-2".
	fake.reset()
	got, err = svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{"web"})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, findResourceById(t, bulk, "web"), findResourceById(t, got, "web"), "targeted fetch must build the bulk sync's row")
	assert.Equal(t, findResourceById(t, bulk, albArn("web")), findResourceById(t, got, albArn("web")))
	assertNoRegionScan()

	// The event processor keys a Classic event on the name; it must adopt the
	// Classic row, not the same-named ALB.
	match, found := findLookupMatch(got, "web")
	require.True(t, found)
	assert.Equal(t, getAwsServiceResourceType(ServiceNameELB, "loadbalancer"), match.Type)

	// DeleteLoadBalancer on the Classic "web": it is gone, the ALB "web" is not.
	// The ALB must not be adopted — it would be marked Deleted.
	classic["web"] = false
	fake.reset()
	got, err = svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{"web"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, albArn("web"), got[0].Id)
	_, found = findLookupMatch(got, "web")
	assert.False(t, found, "a same-named ALB is not the deleted Classic Load Balancer")
	assertNoRegionScan()

	// DeleteLoadBalancer on an ALB: gone. Empty and no error, and one API call.
	fake.reset()
	got, err = svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{albArn("gone")})
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Equal(t, []string{"elbv2:DescribeLoadBalancers"}, fake.ops())

	// A denied lookup is not "gone".
	denyDescribe = true
	_, err = svc.GetResourcesByIds(ctx, account, fakeAwsRegion, []string{albArn("api")})
	require.Error(t, err)
	assert.NotErrorIs(t, err, errors.ErrUnsupported)
}

// When a targeted lookup fails, ListResources falls back to scanning the region.
// That scan must still be filtered to the requested ids: before this was fixed a
// failed lookup skipped the filter and get_resources returned every queue in the
// region for a request about one.
func TestListResourcesFiltersScanWhenTargetedFetchFails(t *testing.T) {
	fake := startFakeAws(t, func(req fakeAwsRequest) (int, string) {
		switch req.Op {
		case "sqs:GetQueueUrl":
			// Not a permission or throttling error, so it is neither retried nor
			// recorded; it is just a failed targeted lookup.
			return 400, `{"__type":"com.amazonaws.sqs#InvalidAddress","message":"invalid"}`
		case "sqs:ListQueues":
			urls := []string{}
			for _, name := range []string{"orders", "billing"} {
				urls = append(urls, fmt.Sprintf("https://sqs.us-east-1.amazonaws.com/%s/%s", fakeAwsAccount, name))
			}
			body, _ := json.Marshal(map[string]any{"QueueUrls": urls})
			return 200, string(body)
		case "sqs:GetQueueAttributes":
			queueURL := req.Params["QueueUrl"]
			return 200, fmt.Sprintf(`{"Attributes":{"QueueArn":"arn:aws:sqs:us-east-1:%s:%s"}}`, fakeAwsAccount, queueURL[strings.LastIndex(queueURL, "/")+1:])
		case "sqs:ListQueueTags":
			return 200, `{"Tags":{}}`
		}
		return 500, `{"__type":"UnexpectedCall"}`
	})

	ctx := &targetedFetchTestCtx{ctx: context.Background()}
	resp, err := (&awsProvider{}).ListResources(ctx, providers.Account{AccountNumber: fakeAwsAccount}, providers.ListResourceRequest{
		ServiceName: ServiceNameSQS,
		Regions:     []string{fakeAwsRegion},
		ResourceIds: []string{"orders"},
	})
	require.NoError(t, err)
	require.Contains(t, fake.ops(), "sqs:ListQueues", "the failed lookup must fall back to the region scan")
	require.Len(t, resp.Items, 1, "the fallback scan must be filtered to the requested ids")
	assert.Equal(t, "orders", resp.Items[0].Id)
}
