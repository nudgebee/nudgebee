package aws

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	"github.com/aws/smithy-go/ptr"
	"github.com/stretchr/testify/require"
	"nudgebee/collector/cloud/providers"
)

type paginatedCloudTrailProvider struct {
	dummyAwsProvider
	lookup func(providers.CloudProviderContext, *cloudtrail.LookupEventsInput) (*cloudtrail.LookupEventsOutput, error)
}

func (d *paginatedCloudTrailProvider) LookupCloudTrailEvents(ctx providers.CloudProviderContext, _ providers.Account, _ string, input *cloudtrail.LookupEventsInput) (*cloudtrail.LookupEventsOutput, error) {
	return d.lookup(ctx, input)
}

func TestCloudTrailActionPaginatesConfiguredIAMQuery(t *testing.T) {
	// The bundled ECR pull-error rule also queries IAM: ListGroups can fill
	// page one while a CreateUser operation sits on page two.
	rules, err := GetEventRules("")
	require.NoError(t, err)
	var action ActionDefinition
	for _, rule := range rules.Rules {
		for _, candidate := range rule.Actions {
			if candidate.Name == "get_cloudtrail_for_ecr_pull_errors" {
				action = candidate
			}
		}
	}
	require.NotEmpty(t, action.Name)
	params := make(map[string]any, len(action.Params))
	for key, value := range action.Params {
		params[key] = value
	}
	action.Params = params
	action.Params["region"] = "us-east-1"
	calls, ecrCalls := 0, 0
	eventTime := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	api := &paginatedCloudTrailProvider{}
	api.lookup = func(ctx providers.CloudProviderContext, input *cloudtrail.LookupEventsInput) (*cloudtrail.LookupEventsOutput, error) {
		calls++
		deadline, ok := ctx.GetContext().Deadline()
		require.True(t, ok)
		require.Equal(t, eventTime.Add(-15*time.Minute), *input.StartTime)
		require.Equal(t, eventTime.Add(5*time.Minute), *input.EndTime)
		if *input.LookupAttributes[0].AttributeValue == "ecr.amazonaws.com" {
			require.WithinDuration(t, time.Now().Add(15*time.Second), deadline, time.Second)
			ecrCalls++
			return &cloudtrail.LookupEventsOutput{NextToken: ptr.String(strconv.Itoa(ecrCalls))}, nil
		}
		require.WithinDuration(t, time.Now().Add(30*time.Second), deadline, time.Second)
		if input.NextToken == nil {
			events := make([]types.Event, 50)
			for i := range events {
				events[i] = types.Event{EventName: ptr.String("ListGroups"), ReadOnly: ptr.String("true")}
			}
			return &cloudtrail.LookupEventsOutput{Events: events, NextToken: ptr.String("iam-page-2")}, nil
		}
		require.Equal(t, "iam-page-2", *input.NextToken)
		events := make([]types.Event, 50)
		for i := range events {
			events[i] = types.Event{EventId: ptr.String(time.Unix(int64(i), 0).String()), EventName: ptr.String("CreateUser"), ReadOnly: ptr.String("false"), EventTime: ptr.Time(eventTime.Add(-time.Duration(i) * time.Second))}
		}
		// A third page exists, but the requested fifty matches have been found.
		return &cloudtrail.LookupEventsOutput{Events: events, NextToken: ptr.String("unneeded-page-3")}, nil
	}
	processor := NewTemplatedEventBridgeProcessor(rules, api)
	result, err := processor.executeAction(providers.NewCloudProviderContext(context.Background()), providers.Account{}, EventBridgeEvent{Time: eventTime, Region: "us-east-1"}, action, nil)
	require.NoError(t, err)
	events := result.([]types.Event)
	require.Len(t, events, 50)
	require.Equal(t, "CreateUser", *events[0].EventName)
	require.Equal(t, eventTime, *events[0].EventTime)
	require.Equal(t, 5, ecrCalls)
	require.Equal(t, 7, calls)
}

func TestCloudTrailActionPaginationFailure(t *testing.T) {
	for _, failure := range []string{"error", "cancelled", "nil", "repeated-token"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			api := &paginatedCloudTrailProvider{}
			api.lookup = func(_ providers.CloudProviderContext, input *cloudtrail.LookupEventsInput) (*cloudtrail.LookupEventsOutput, error) {
				calls++
				if calls == 1 {
					if failure == "cancelled" {
						cancel()
					}
					return &cloudtrail.LookupEventsOutput{NextToken: ptr.String("page2")}, nil
				}
				switch failure {
				case "error":
					return nil, errors.New("lookup failed")
				case "nil":
					return nil, nil
				default:
					return &cloudtrail.LookupEventsOutput{NextToken: input.NextToken}, nil
				}
			}
			processor := NewTemplatedEventBridgeProcessor(EventRuleSet{}, api)
			result, err := processor.executeAction(providers.NewCloudProviderContext(ctx), providers.Account{}, EventBridgeEvent{Time: time.Now()}, ActionDefinition{Name: "test", Type: "aws_get_cloudtrail_events", Params: map[string]any{"start_time_offset": "-15m", "end_time_offset": "+5m", "lookup_attributes": []any{map[string]any{"AttributeKey": "EventSource", "AttributeValue": "iam.amazonaws.com"}}}}, nil)
			require.Error(t, err)
			require.Nil(t, result)
			if failure == "cancelled" {
				require.ErrorIs(t, err, context.Canceled)
				require.Equal(t, 1, calls)
			} else {
				require.Equal(t, 2, calls)
			}
		})
	}
}

func TestCloudTrailActionFiltersAndMergesPages(t *testing.T) {
	now := time.Now()
	calls := 0
	api := &paginatedCloudTrailProvider{}
	api.lookup = func(_ providers.CloudProviderContext, input *cloudtrail.LookupEventsInput) (*cloudtrail.LookupEventsOutput, error) {
		calls++
		if input.NextToken == nil {
			return &cloudtrail.LookupEventsOutput{Events: []types.Event{
				{EventName: ptr.String("DeleteRole"), ReadOnly: ptr.String("false")},
				{EventName: ptr.String("CreateUser"), ReadOnly: ptr.String("true")},
			}, NextToken: ptr.String("page2")}, nil
		}
		id, age := "older", 2*time.Second
		if *input.LookupAttributes[0].AttributeValue == "second" {
			id, age = "middle", time.Second
		}
		return &cloudtrail.LookupEventsOutput{Events: []types.Event{
			{EventId: ptr.String("newest"), EventName: ptr.String("CreateUser"), EventTime: ptr.Time(now)},
			{EventId: ptr.String(id), EventName: ptr.String("CreateUser"), EventTime: ptr.Time(now.Add(-age))},
		}, NextToken: ptr.String("unneeded-page3")}, nil
	}
	processor := NewTemplatedEventBridgeProcessor(EventRuleSet{}, api)
	result, err := processor.executeAction(providers.NewCloudProviderContext(context.Background()), providers.Account{}, EventBridgeEvent{Time: now}, ActionDefinition{Name: "test", Type: "aws_get_cloudtrail_events", Params: map[string]any{
		"max_results": 2, "start_time_offset": "-15m", "end_time_offset": "+5m",
		"lookup_attributes": []any{
			map[string]any{"AttributeKey": "EventSource", "AttributeValue": "first", "event_names": []string{"CreateUser"}},
			map[string]any{"AttributeKey": "EventSource", "AttributeValue": "second", "event_names": []string{"CreateUser"}},
		},
	}}, nil)
	require.NoError(t, err)
	events := result.([]types.Event)
	require.Len(t, events, 2)
	require.Equal(t, "newest", *events[0].EventId)
	require.Equal(t, "middle", *events[1].EventId)
	require.Equal(t, 4, calls)
}

func TestCloudTrailActionPreservesEvidenceAfterLaterPageTimeout(t *testing.T) {
	api := &paginatedCloudTrailProvider{}
	api.lookup = func(_ providers.CloudProviderContext, input *cloudtrail.LookupEventsInput) (*cloudtrail.LookupEventsOutput, error) {
		if input.NextToken != nil {
			return nil, context.DeadlineExceeded
		}
		return &cloudtrail.LookupEventsOutput{
			Events:    []types.Event{{EventId: ptr.String("page-one"), EventName: ptr.String("CreateUser")}},
			NextToken: ptr.String("page-two"),
		}, nil
	}
	processor := NewTemplatedEventBridgeProcessor(EventRuleSet{}, api)
	result, err := processor.executeAction(providers.NewCloudProviderContext(context.Background()), providers.Account{}, EventBridgeEvent{Time: time.Now()}, ActionDefinition{
		Name: "test", Type: "aws_get_cloudtrail_events",
		Params: map[string]any{
			"start_time_offset": "-15m", "end_time_offset": "+5m",
			"lookup_attributes": []any{map[string]any{"AttributeKey": "EventSource", "AttributeValue": "iam.amazonaws.com"}},
		},
	}, nil)
	require.NoError(t, err)
	events := result.([]types.Event)
	require.Len(t, events, 1)
	require.Equal(t, "page-one", *events[0].EventId)
}
