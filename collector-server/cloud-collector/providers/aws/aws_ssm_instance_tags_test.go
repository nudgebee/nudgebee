package aws

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

type fakeSSMListTags struct {
	calls []string
	tags  map[string][]types.Tag
	err   error
}

func (f *fakeSSMListTags) ListTagsForResource(_ context.Context, in *ssm.ListTagsForResourceInput, _ ...func(*ssm.Options)) (*ssm.ListTagsForResourceOutput, error) {
	f.calls = append(f.calls, aws.ToString(in.ResourceId))
	if f.err != nil {
		return nil, f.err
	}
	return &ssm.ListTagsForResourceOutput{TagList: f.tags[aws.ToString(in.ResourceId)]}, nil
}

type fakeEC2DescribeTags struct {
	calls     [][]string
	tags      []ec2types.TagDescription
	err       error
	pageLimit int // tags per page; 0 = all in one page
}

func (f *fakeEC2DescribeTags) DescribeTags(_ context.Context, in *ec2.DescribeTagsInput, _ ...func(*ec2.Options)) (*ec2.DescribeTagsOutput, error) {
	if f.err != nil {
		return nil, f.err
	}
	var ids []string
	for _, flt := range in.Filters {
		if aws.ToString(flt.Name) == "resource-id" {
			ids = flt.Values
		}
	}
	if in.NextToken == nil {
		f.calls = append(f.calls, ids)
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var matched []ec2types.TagDescription
	for _, t := range f.tags {
		if want[aws.ToString(t.ResourceId)] {
			matched = append(matched, t)
		}
	}
	start := 0
	if in.NextToken != nil {
		start, _ = strconv.Atoi(*in.NextToken)
	}
	end := len(matched)
	var next *string
	if f.pageLimit > 0 && start+f.pageLimit < len(matched) {
		end = start + f.pageLimit
		next = aws.String(strconv.Itoa(end))
	}
	return &ec2.DescribeTagsOutput{Tags: matched[start:end], NextToken: next}, nil
}

func ec2Tag(id, k, v string) ec2types.TagDescription {
	return ec2types.TagDescription{ResourceId: aws.String(id), Key: aws.String(k), Value: aws.String(v)}
}

func TestFetchSSMManagedInstanceTags_RoutesByIDForm(t *testing.T) {
	ssmFake := &fakeSSMListTags{tags: map[string][]types.Tag{
		"mi-0123456789abcdef0": {{Key: aws.String("env"), Value: aws.String("onprem")}},
	}}
	ec2Fake := &fakeEC2DescribeTags{pageLimit: 1, tags: []ec2types.TagDescription{
		ec2Tag("i-aaa", "team", "payments"),
		ec2Tag("i-aaa", "env", "prod"),
		ec2Tag("i-other", "env", "ignored"),
	}}
	failures := &ssmInstanceTagFailures{}

	got := fetchSSMManagedInstanceTags(context.Background(), ssmFake, ec2Fake,
		[]string{"mi-0123456789abcdef0", "i-aaa", "i-bbb", "edge-node"}, failures)

	if len(ssmFake.calls) != 1 || ssmFake.calls[0] != "mi-0123456789abcdef0" {
		t.Fatalf("SSM ListTagsForResource must be called only for mi- IDs, got %v", ssmFake.calls)
	}
	if len(ec2Fake.calls) != 1 || len(ec2Fake.calls[0]) != 2 {
		t.Fatalf("EC2 DescribeTags must be called once with the i- IDs, got %v", ec2Fake.calls)
	}
	if v := got["mi-0123456789abcdef0"]["env"]; len(v) != 1 || v[0] != "onprem" {
		t.Errorf("hybrid instance tags = %v", got["mi-0123456789abcdef0"])
	}
	if got["i-aaa"]["team"][0] != "payments" || got["i-aaa"]["env"][0] != "prod" {
		t.Errorf("EC2 instance tags across pages = %v", got["i-aaa"])
	}
	if tags, ok := got["i-bbb"]; !ok || len(tags) != 0 {
		t.Errorf("untagged EC2 instance should get an empty tag map, got %v (present=%v)", tags, ok)
	}
	if _, ok := got["edge-node"]; ok {
		t.Errorf("unknown ID form should be skipped")
	}
	if failures.count != 0 {
		t.Errorf("no failures expected, got %d (%v)", failures.count, failures.firstErr)
	}
}

func TestFetchSSMManagedInstanceTags_BatchesEC2IDs(t *testing.T) {
	ids := make([]string, ec2DescribeTagsMaxIDs+1)
	for i := range ids {
		ids[i] = "i-" + strconv.Itoa(i)
	}
	ec2Fake := &fakeEC2DescribeTags{}
	fetchSSMManagedInstanceTags(context.Background(), &fakeSSMListTags{}, ec2Fake, ids, &ssmInstanceTagFailures{})
	if len(ec2Fake.calls) != 2 || len(ec2Fake.calls[0]) != ec2DescribeTagsMaxIDs || len(ec2Fake.calls[1]) != 1 {
		t.Fatalf("expected batches of %d and 1, got %d batches", ec2DescribeTagsMaxIDs, len(ec2Fake.calls))
	}
}

func TestFetchSSMManagedInstanceTags_CountsFailures(t *testing.T) {
	boom := errors.New("throttled")
	failures := &ssmInstanceTagFailures{}
	got := fetchSSMManagedInstanceTags(context.Background(),
		&fakeSSMListTags{err: boom}, &fakeEC2DescribeTags{err: boom},
		[]string{"mi-0123456789abcdef0", "i-aaa", "i-bbb"}, failures)

	if failures.count != 3 || !errors.Is(failures.firstErr, boom) {
		t.Fatalf("failures = %d / %v, want 3 / throttled", failures.count, failures.firstErr)
	}
	if len(got) != 0 {
		t.Errorf("failed lookups must not store tags, got %v", got)
	}
}
