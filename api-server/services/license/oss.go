package license

import "time"

// ossLicense is the default impl shipped in every binary. The OSS build has
// no EE package to override it. In EE builds, ee/license/jwt.go registers a
// JWT-backed impl that replaces this default at process start.
type ossLicense struct{}

func (ossLicense) Tier() Tier               { return TierOSS }
func (ossLicense) Status() Status           { return StatusMissing }
func (ossLicense) HasFeature(_ string) bool { return false }
func (ossLicense) AllFeatures() []string    { return nil }
func (ossLicense) TenantID() string         { return "" }
func (ossLicense) Email() string            { return "" }
func (ossLicense) Expiry() time.Time        { return time.Time{} }
func (ossLicense) MaxAccounts() int         { return -1 }
func (ossLicense) MaxNodesPerCluster() int  { return -1 }
