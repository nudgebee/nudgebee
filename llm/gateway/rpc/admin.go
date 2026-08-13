package rpc

import "github.com/gin-gonic/gin"

// adminRegistrar is an optionally-registered factory that mounts the admin config
// plane. When none is registered it is nil and AdminRoutes returns nil, so the OSS
// build (with the ee/ subtree removed) never mounts the admin routes. The EE admin
// package registers itself via RegisterAdminRoutes in its init().
var adminRegistrar func(r *gin.Engine, token string)

// RegisterAdminRoutes registers the admin-plane mount function. Called from the EE
// admin package's init(); main does not reference it.
func RegisterAdminRoutes(fn func(r *gin.Engine, token string)) { adminRegistrar = fn }

// AdminRoutes returns the registered admin-plane mount function, or nil when none is
// registered (OSS build — no admin config CRUD). main calls it after the usage plane
// and invokes the result only when non-nil.
func AdminRoutes() func(r *gin.Engine, token string) { return adminRegistrar }
