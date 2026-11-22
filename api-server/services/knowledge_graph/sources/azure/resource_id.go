package azure

import "strings"

// What makes an Azure resource's name unique, read off its resource id.
//
// Azure guarantees a name is unique within its resource group and type, and a
// child resource's name within its parent and its own type: every storage
// account has a blob, a file, a queue and a table service, all four called
// "default"; two servers can each hold a database called "orders"; the same
// private DNS zone name is reused across resource groups. The node key
// therefore carries the resource group where AWS carries the VPC and GCP the
// project, and a child resource is named by its path in the resource id below
// the top-level resource ("shopstore/blobservices/default",
// "sqlsrv1/databases/orders").

// azureKeyParts returns the resource group and the key name for a resource.
//
// The resource group is lowercased: Azure treats it case-insensitively and the
// collector, the CLI and the Resource Graph report it in different cases. It is
// empty for resources above any resource group (subscription- or tenant-level).
//
// The key name is rowName for a top-level resource. For a child resource it is
// the id's path below the top-level resource, lowercased, ending in rowName's
// own segment: the id is stored lowercased while rowName keeps the case the
// collector reports. Extension resources (a second "/providers/" segment under
// another resource) count the resource they extend as a parent.
func azureKeyParts(resourceID, rowName string) (resourceGroup, keyName string) {
	keyName = rowName
	segments := strings.Split(strings.Trim(resourceID, "/"), "/")

	// Collect the type/name pairs after each "providers/<namespace>".
	var path []string // type, name, type, name, …
	for i := 0; i < len(segments); i++ {
		seg := strings.ToLower(segments[i])
		switch {
		case seg == "resourcegroups" && i+1 < len(segments):
			resourceGroup = strings.ToLower(segments[i+1])
			i++
		case seg == "providers" && i+1 < len(segments):
			i += 2 // skip "providers" and the namespace
			for ; i+1 < len(segments) && !strings.EqualFold(segments[i], "providers"); i += 2 {
				path = append(path, segments[i], segments[i+1])
			}
			i-- // let the outer loop see the next "providers", if any
		}
	}

	if len(path) < 4 {
		if keyName == "" && len(path) == 2 {
			keyName = path[1]
		}
		// A name typed as a path (a declared node has no id) follows the same
		// rule: the path lowercased, the resource's own name as given.
		if i := strings.LastIndex(keyName, "/"); i > 0 {
			keyName = strings.ToLower(keyName[:i]) + keyName[i:]
		}
		return resourceGroup, keyName
	}
	// Below the top-level resource: its name, then each child's type and name.
	parents := strings.ToLower(strings.Join(path[1:len(path)-1], "/"))
	if keyName == "" {
		keyName = path[len(path)-1]
	}
	// Only the row name's own segment is used: some rows already carry part
	// of the path ("server/db"), and an Azure resource name cannot contain "/".
	if i := strings.LastIndex(keyName, "/"); i >= 0 {
		keyName = keyName[i+1:]
	}
	return resourceGroup, parents + "/" + keyName
}

// normalizeAzureRegion folds the spellings the collector stores for one region
// ("South Central US", "southcentralus") into the region code.
func normalizeAzureRegion(region string) string {
	return strings.ToLower(strings.ReplaceAll(region, " ", ""))
}
