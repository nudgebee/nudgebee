package aws

import (
	"context"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

// ssmListTagsAPI is the SSM call used to tag hybrid (mi-) managed instances.
type ssmListTagsAPI interface {
	ListTagsForResource(ctx context.Context, params *ssm.ListTagsForResourceInput, optFns ...func(*ssm.Options)) (*ssm.ListTagsForResourceOutput, error)
}

// ec2DescribeTagsMaxIDs caps the resource-id filter values sent in one
// DescribeTags request.
const ec2DescribeTagsMaxIDs = 200

// ssmInstanceTagFailures counts tag lookups that failed during one crawl, so the
// caller logs them once instead of once per instance.
type ssmInstanceTagFailures struct {
	count    int
	firstErr error
}

func (f *ssmInstanceTagFailures) add(n int, err error) {
	if n == 0 {
		return
	}
	f.count += n
	if f.firstErr == nil {
		f.firstErr = err
	}
}

// fetchSSMManagedInstanceTags returns tags keyed by instance ID for the IDs that
// DescribeInstanceInformation returned.
//
// SSM's ListTagsForResource with ResourceType ManagedInstance accepts only
// hybrid node IDs (mi-...). EC2 instance IDs (i-...) are rejected with a
// ValidationException, so their tags are read from EC2 in batches instead.
// IDs of any other form are skipped.
func fetchSSMManagedInstanceTags(ctx context.Context, ssmAPI ssmListTagsAPI, ec2API ec2.DescribeTagsAPIClient, ids []string, failures *ssmInstanceTagFailures) map[string]map[string][]string {
	result := make(map[string]map[string][]string, len(ids))
	ec2IDs := []string{}

	for _, id := range ids {
		switch {
		case strings.HasPrefix(id, "mi-"):
			out, err := ssmAPI.ListTagsForResource(ctx, &ssm.ListTagsForResourceInput{
				ResourceType: types.ResourceTypeForTaggingManagedInstance,
				ResourceId:   aws.String(id),
			})
			if err != nil {
				failures.add(1, err)
				continue
			}
			tags := make(map[string][]string)
			for _, tag := range out.TagList {
				if tag.Key != nil && tag.Value != nil {
					tags[*tag.Key] = append(tags[*tag.Key], *tag.Value)
				}
			}
			result[id] = tags
		case strings.HasPrefix(id, "i-"):
			ec2IDs = append(ec2IDs, id)
		}
	}

	for start := 0; start < len(ec2IDs); start += ec2DescribeTagsMaxIDs {
		batch := ec2IDs[start:min(start+ec2DescribeTagsMaxIDs, len(ec2IDs))]
		batchTags := make(map[string]map[string][]string, len(batch))
		paginator := ec2.NewDescribeTagsPaginator(ec2API, &ec2.DescribeTagsInput{
			Filters: []ec2types.Filter{
				{Name: aws.String("resource-type"), Values: []string{"instance"}},
				{Name: aws.String("resource-id"), Values: batch},
			},
		})
		var batchErr error
		for paginator.HasMorePages() {
			page, err := paginator.NextPage(ctx)
			if err != nil {
				batchErr = err
				break
			}
			for _, tag := range page.Tags {
				if tag.ResourceId == nil || tag.Key == nil || tag.Value == nil {
					continue
				}
				if batchTags[*tag.ResourceId] == nil {
					batchTags[*tag.ResourceId] = make(map[string][]string)
				}
				batchTags[*tag.ResourceId][*tag.Key] = append(batchTags[*tag.ResourceId][*tag.Key], *tag.Value)
			}
		}
		if batchErr != nil {
			// A partial page set would store some instances with missing tags;
			// drop the whole batch and report it.
			failures.add(len(batch), batchErr)
			continue
		}
		for _, id := range batch {
			if tags, ok := batchTags[id]; ok {
				result[id] = tags
			} else {
				result[id] = make(map[string][]string)
			}
		}
	}

	return result
}
