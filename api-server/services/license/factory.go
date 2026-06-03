package license

import "sync/atomic"

// holder wraps the License interface so atomic.Value always sees the same
// concrete type (*holder), regardless of which impl is registered.
//
// Without the wrapper, atomic.Value would panic on the second Store: it
// pins the concrete dynamic type of the FIRST stored value. If init()
// seeds with `License(ossLicense{})` (dynamic type ossLicense) and
// ee/license/jwt.go then registers `&jwtLicense{}` or
// `&deploymentModeLicense{}` (different dynamic types), atomic.Value
// rejects the swap. Wrapping in *holder makes every Store homogeneous.
type holder struct{ impl License }

// instance holds the active License impl. Defaults to ossLicense; EE init()
// swaps in a JWT-backed or deployment-mode-driven impl via Register.
var instance atomic.Value

func init() {
	instance.Store(&holder{impl: ossLicense{}})
}

// Get returns the active license. Safe for concurrent reads.
func Get() License {
	return instance.Load().(*holder).impl
}

// Register swaps in a new License impl. Safe for concurrent Get() callers
// (atomic.Value handles the store/load), but called from ee/license init()
// during process start before routes serve so the swap is observable to
// every later request.
func Register(l License) {
	instance.Store(&holder{impl: l})
}
