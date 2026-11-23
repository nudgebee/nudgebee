package account

import (
	"reflect"
	"testing"

	awsProvider "nudgebee/collector/cloud/providers/aws"

	"github.com/samber/lo"
)

func TestArchivableRegions(t *testing.T) {
	cases := []struct {
		name    string
		queried []string
		skipped []string
		want    []string
	}{
		{"nothing skipped", []string{"us-east-1", "eu-west-1"}, nil, []string{"us-east-1", "eu-west-1"}},
		{"one region skipped", []string{"us-east-1", "me-south-1", "eu-west-1"}, []string{"me-south-1"}, []string{"us-east-1", "eu-west-1"}},
		{"all regions skipped yields empty, not nil-scope", []string{"me-south-1", "me-central-1"}, []string{"me-south-1", "me-central-1"}, []string{}},
		{"skipped region not in queried is a no-op", []string{"us-east-1"}, []string{"me-south-1"}, []string{"us-east-1"}},
		{"empty queried stays empty", []string{}, []string{"me-south-1"}, []string{}},
		{"nil queried stays nil", nil, []string{"me-south-1"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := archivableRegions(c.queried, c.skipped)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("archivableRegions(%v, %v) = %v, want %v", c.queried, c.skipped, got, c.want)
			}
		})
	}
}

func TestResolveArchiveRegions(t *testing.T) {
	cases := []struct {
		name    string
		crawled []string
		archive []string
		want    []string
	}{
		{
			// No allowlist in play: archival reconciles exactly what was crawled.
			name:    "no widening falls back to the crawl set",
			crawled: []string{"ap-southeast-2"},
			archive: nil,
			want:    []string{"ap-southeast-2"},
		},
		{
			name:    "empty widening falls back to the crawl set",
			crawled: []string{"ap-southeast-2"},
			archive: []string{},
			want:    []string{"ap-southeast-2"},
		},
		{
			// A region dropped from the allowlist is no longer crawled, so it must
			// stay in the archival scope — otherwise its rows are never observed
			// absent and stay is_active = true forever.
			name:    "de-listed region stays in archival scope",
			crawled: []string{"ap-southeast-2"},
			archive: []string{"ap-southeast-2", "us-east-1"},
			want:    []string{"ap-southeast-2", "us-east-1"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveArchiveRegions(c.crawled, c.archive)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("resolveArchiveRegions(%v, %v) = %v, want %v", c.crawled, c.archive, got, c.want)
			}
		})
	}
}

func TestBoundRequestedRegions(t *testing.T) {
	allowed := []string{"us-east-1", "us-east-2", "us-west-1", "us-west-2"}
	cases := []struct {
		name        string
		requested   []string
		wantCrawl   []string
		wantArchive []string
	}{
		{
			// The case that froze services nightly: rows in regions the
			// account's org policy denies were crawled, and the denial failed
			// the whole service.
			name:        "regions outside the allowlist are not crawled but are archived",
			requested:   []string{"us-east-1", "eu-west-1", "ap-south-1", "us-west-2"},
			wantCrawl:   []string{"us-east-1", "us-west-2"},
			wantArchive: []string{"us-east-1", "eu-west-1", "ap-south-1", "us-west-2"},
		},
		{
			name:        "a single allowed region is crawled and archived alone",
			requested:   []string{"us-west-2"},
			wantCrawl:   []string{"us-west-2"},
			wantArchive: []string{"us-west-2"},
		},
		{
			name:        "no allowed region leaves nothing to crawl",
			requested:   []string{"eu-west-1"},
			wantCrawl:   []string{},
			wantArchive: []string{"eu-west-1"},
		},
		{
			// The nightly sync adds the allowlist to the regions a service has
			// rows in, so a newly allowed region is crawled for it too.
			name:        "nightly request: rows' regions plus the allowlist",
			requested:   lo.Union([]string{"us-east-1", "eu-west-1", "global"}, allowed),
			wantCrawl:   []string{"us-east-1", "us-east-2", "us-west-1", "us-west-2"},
			wantArchive: []string{"us-east-1", "eu-west-1", "global", "us-east-2", "us-west-1", "us-west-2"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			crawl, archive := boundRequestedRegions(c.requested, allowed)
			if !reflect.DeepEqual(crawl, c.wantCrawl) {
				t.Fatalf("crawl = %v, want %v", crawl, c.wantCrawl)
			}
			if !reflect.DeepEqual(archive, c.wantArchive) {
				t.Fatalf("archive = %v, want %v", archive, c.wantArchive)
			}
		})
	}
}

// A service the AWS provider lists once, account-wide, must also be global here.
// Otherwise its crawl keeps a region list, the provider filters its items
// (stored as region "global") against that list, and every row is retired.
func TestProviderGlobalServicesAreGlobalHere(t *testing.T) {
	for _, name := range []string{
		awsProvider.ServiceNameS3,
		awsProvider.ServiceNameIAM,
		awsProvider.ServiceNameRoute53,
		awsProvider.ServiceNameCloudFront,
	} {
		svc, ok := awsProvider.GetAwsService(name)
		if !ok || !svc.IsGlobal() {
			t.Fatalf("%s: expected the AWS provider to list it account-wide", name)
		}
		if !isGlobalAwsService(name) {
			t.Fatalf("%s is listed account-wide by the provider but is not in globalServices", name)
		}
	}
}
