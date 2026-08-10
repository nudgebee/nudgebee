// Package vmpackage pulls a VM's installed package inventory via forager's
// discovery_inventory action (through the existing forager proxy relay),
// stores it in vm_package, matches it against vuln-matcher-server, and
// records findings in the recommendation table. Scope is intentionally
// narrow: the caller already knows which cloud_resource the scan is for —
// asset discovery/identity/merge across sources is a separate, larger
// effort tracked in issue #35405.
package vmpackage

// ScanRequest is the manual trigger's input: which VM (cloud_resource_id) to
// scan, reached via which forager datasource (datasource_id), under which
// account.
type ScanRequest struct {
	AccountId       string `json:"account_id" mapstructure:"account_id" validate:"required"`
	DatasourceId    string `json:"datasource_id" mapstructure:"datasource_id" validate:"required"`
	CloudResourceId string `json:"cloud_resource_id" mapstructure:"cloud_resource_id" validate:"required"`
}

// ScanResponse acknowledges that the scan started. The scan itself runs
// detached (see ScanPackages); this call does not wait for it to finish.
type ScanResponse struct {
	Data []map[string]any `json:"data" mapstructure:"data"`
}

// Package types accepted by vuln-matcher-server for this slice — apk and
// Windows packages are out of scope until forager collects them.
const (
	PkgTypeDeb = "deb"
	PkgTypeRPM = "rpm"
)

// Package is one installed package as parsed from a package manager's raw
// output, before it's written to vm_package.
type Package struct {
	Type string
	Name string

	// Version is kept byte-exact as reported by the package manager (deb
	// keeps any epoch prefix inline; rpm is version-release, e.g.
	// "3.0.7-24.el9_1" — the release suffix matters for CVE matching).
	Version string
	Arch    string

	// Epoch is nil when the package manager reports no epoch (rpm's
	// "(none)"), which is NOT the same as an explicit epoch of 0.
	Epoch *int

	// SourceName is the source/origin package. Required by vuln-matcher-server
	// for deb/rpm — omitting it silently under-reports findings rather than
	// erroring, so every parser must always populate it.
	SourceName    string
	SourceVersion string
}
