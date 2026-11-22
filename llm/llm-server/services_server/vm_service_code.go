package services_server

import (
	"fmt"
	"nudgebee/llm/common"
	"strings"
)

// VMServiceCodeLink is the repository and branch a user linked to one service
// (a systemd unit or container) on a VM. The link is stored as two
// cloud_resource_attributes rows on the VM's inventory row, named after the
// workload git annotations with the service's node-agent container_id appended
// (e.g. "workloads.nudgebee.com/git.repo//system.slice/nginx.service").
// Clearing a link writes an empty value, so empty means unset.
//
// The key format is built independently by serviceAnnotationKey in
// app/src/lib/annotationKeys.ts; change both together.
type VMServiceCodeLink struct {
	Repo   string
	Branch string
}

type vmServiceAttribute struct {
	Name  string `db:"name"`
	Value string `db:"value"`
}

// GetVMServiceCodeLink returns the link for the service of the VM that eventId
// is about. A zero link means none is set, not an error.
func GetVMServiceCodeLink(dbManager *common.DatabaseManager, eventId, containerID string) (VMServiceCodeLink, error) {
	repoKey := AnnotationGitRepo + "/" + containerID
	branchKey := AnnotationGitBranch + "/" + containerID

	var attrs []vmServiceAttribute
	err := dbManager.Db.Select(&attrs, `
		SELECT cra.name, COALESCE(cra.value, '') AS value
		FROM events e
		JOIN cloud_resource_attributes cra ON cra.resource_id = e.cloud_resource_id
		WHERE e.id = $1 AND cra.name IN ($2, $3)`, eventId, repoKey, branchKey)
	if err != nil {
		return VMServiceCodeLink{}, fmt.Errorf("GetVMServiceCodeLink: failed to read links for event %s: %w", eventId, err)
	}

	var link VMServiceCodeLink
	for _, a := range attrs {
		switch a.Name {
		case repoKey:
			link.Repo = strings.TrimSpace(a.Value)
		case branchKey:
			link.Branch = strings.TrimSpace(a.Value)
		}
	}
	return link, nil
}

// VMServiceLink is one linked service of an account's VMs.
type VMServiceLink struct {
	VM      string
	Service string
	VMServiceCodeLink
}

type vmServiceLinkRow struct {
	VM      string `db:"vm"`
	Service string `db:"service"`
	Repo    string `db:"repo"`
	Branch  string `db:"branch"`
}

// maxVMServiceLinks bounds the linked services read for a prompt.
const maxVMServiceLinks = 100

// ListVMServiceCodeLinks returns every service of the account's VMs that has a
// repository linked, ordered by VM then service. The cap counts links, not
// attribute rows: it is applied to the repo rows, and each one's branch is
// joined on, so a service never keeps a branch and loses its repo.
func ListVMServiceCodeLinks(dbManager *common.DatabaseManager, accountId string) ([]VMServiceLink, error) {
	repoPrefix := AnnotationGitRepo + "/"
	var rows []vmServiceLinkRow
	err := dbManager.Db.Select(&rows, `
		SELECT r.vm, r.service, r.repo, COALESCE(b.value, '') AS branch
		FROM (
			SELECT cr.name AS vm, cra.resource_id, substr(cra.name, $2) AS service, cra.value AS repo
			FROM cloud_resource_attributes cra
			JOIN cloud_resourses cr ON cr.id = cra.resource_id
			WHERE cr.account = $1 AND cr.is_active IS TRUE
			  AND starts_with(cra.name, $3) AND COALESCE(cra.value, '') <> ''
			ORDER BY cr.name, cra.name
			LIMIT $5
		) r
		LEFT JOIN cloud_resource_attributes b ON b.resource_id = r.resource_id AND b.name = $4 || r.service
		ORDER BY r.vm, r.service`,
		accountId, len(repoPrefix)+1, repoPrefix, AnnotationGitBranch+"/", maxVMServiceLinks)
	if err != nil {
		return nil, fmt.Errorf("ListVMServiceCodeLinks: failed to read links for account %s: %w", accountId, err)
	}

	var links []VMServiceLink
	for _, r := range rows {
		repo := strings.TrimSpace(r.Repo)
		if repo == "" {
			continue
		}
		links = append(links, VMServiceLink{VM: r.VM, Service: r.Service, VMServiceCodeLink: VMServiceCodeLink{Repo: repo, Branch: strings.TrimSpace(r.Branch)}})
	}
	return links, nil
}
