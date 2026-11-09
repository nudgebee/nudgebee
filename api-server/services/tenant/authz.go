package tenant

// eventually move to security package
// currently its here because of circular dependency caused by feature flag apis

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/security"
)

// categoryCustomGrantModule maps a validate_access `category` to the dynamic-RBAC
// permission module whose custom-role grant may additionally satisfy the check
// (account-scoped, via HasScopedPermission — operation-surface widening only).
// Categories absent here stay built-in-role-only: notably ACCOUNTS (the
// relay/grafana proxy access check in app/src/lib/accountAccess.ts) must remain
// gated on a real account role, per the accounts-catalog "visible-but-inert"
// decision in docs/architecture-decisions.md — a custom accounts:Read grant
// enumerates the catalog but must NOT unlock per-account operational access.
var categoryCustomGrantModule = map[string]string{
	"TICKETS": "tickets",
}

// hasCustomCategoryAccess reports whether a dynamic-RBAC custom-role grant
// authorizes `permission` on `category` for `accountId`. Read is satisfied by a
// Read or Write grant (Write implies Read); create/update/delete require Write.
// Returns false for categories not opted in above, so unmapped categories keep
// their built-in-role-only behavior unchanged.
func hasCustomCategoryAccess(sc *security.SecurityContext, accountId, category string, permission security.SecurityAccessType) bool {
	module, ok := categoryCustomGrantModule[category]
	if !ok || accountId == "" {
		return false
	}
	switch permission {
	case security.SecurityAccessTypeRead:
		return sc.HasScopedPermission(accountId, module, "Read") || sc.HasScopedPermission(accountId, module, "Write")
	case security.SecurityAccessTypeCreate, security.SecurityAccessTypeUpdate, security.SecurityAccessTypeDelete:
		return sc.HasScopedPermission(accountId, module, "Write")
	default:
		return false
	}
}

// hasRoleAccess is the role half of ValidateAccess. It assumes accountId, when
// set, has already been checked to belong to the context's tenant.
func hasRoleAccess(sc *security.SecurityContext, accountId, category string, permission security.SecurityAccessType) bool {
	if sc.IsTenantAdmin() {
		return true
	}
	if sc.IsTenantReadAdmin() && permission == security.SecurityAccessTypeRead {
		return true
	}
	if accountId != "" && sc.HasAccountAccess(accountId, permission) {
		return true
	}
	if hasCustomCategoryAccess(sc, accountId, category, permission) {
		// Dynamic-RBAC: a custom-role grant for this category's module
		// (currently only TICKETS→tickets) authorizes the operation even
		// without a built-in account role. Account-scoped; never widens
		// which accounts exist for the user.
		return true
	}
	return false
}

func ValidateAccess(request ValidateAccessRequest) (ValidateAccessResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return ValidateAccessResponse{}, err
	}

	response := ValidateAccessResponse{
		Access: []ValidateAccessResponseAccess{},
	}

	securityContexts := map[string]*security.SecurityContext{}

	for _, access := range request.Access {
		securityContext, ok := securityContexts[access.TenantId]
		if !ok {
			tenantSecurityContext, err := security.NewSecurityContext(access.TenantId, request.UserId)
			if err != nil {
				response.Access = append(response.Access, ValidateAccessResponseAccess{
					Allowed: false,
					Message: err.Error(),
				})
				continue
			}
			securityContext = tenantSecurityContext
			securityContexts[access.TenantId] = tenantSecurityContext
		}

		// The tenant shortcuts below answer "may this user act in access.TenantId",
		// not "is this account in access.TenantId". Without this gate a tenant
		// admin of any tenant — every SaaS self-signup is one — passed for another
		// tenant's account id, and the relay/grafana proxies then forwarded to that
		// tenant's agent. Runs before every branch, the K8s RBAC one included.
		if access.Args.AccountId != "" && !securityContext.IsTenantAccount(access.Args.AccountId) {
			response.Access = append(response.Access, ValidateAccessResponseAccess{
				Allowed: false,
				Message: "account does not belong to tenant",
			})
			continue
		}

		allowed := hasRoleAccess(securityContext, access.Args.AccountId, string(access.Category), access.Permission)
		if IsFeatureEnabled(security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil), access.TenantId, FEATURE_RBACK_K8S_ACCESS) && access.Args.K8sObjectName != "" && access.Args.K8sObjectType != "" {
			// The K8s RBAC answer replaces the role answer. One result per item:
			// callers read the response positionally.
			k8sAllowed, err := securityContext.HasK8sAccess(access.Args.AccountId, access.Args.K8sObjectType, access.Args.K8sObjectName, security.K8sRbacPermissionType(string(access.Permission)))
			if err != nil {
				response.Access = append(response.Access, ValidateAccessResponseAccess{
					Allowed: false,
					Message: err.Error(),
				})
				continue
			}
			allowed = k8sAllowed
		}
		response.Access = append(response.Access, ValidateAccessResponseAccess{
			Allowed: allowed,
		})
	}

	return response, nil
}

func GetK8sRoles(request GetK8sRolesRequest) (GetK8sRolesResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return GetK8sRolesResponse{}, err
	}

	securityContext, err := security.NewSecurityContext(request.TenantId, request.UserId)
	if err != nil {
		return GetK8sRolesResponse{}, err
	}

	if !IsFeatureEnabled(nil, request.TenantId, FEATURE_RBACK_K8S_ACCESS) {
		return GetK8sRolesResponse{
			Enabled: false,
		}, nil
	}

	roles, err := securityContext.ListK8sPermissions(request.AccountId, request.K8sObjectType, request.K8sObjectNames)
	if err != nil {
		return GetK8sRolesResponse{
			Enabled: true,
		}, err
	}

	return GetK8sRolesResponse{
		Enabled:     true,
		ObjectRoles: roles,
	}, nil
}

func GetK8sObjectNames(request GetK8sObjectNamesRequest) (GetK8sObjectNamesResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return GetK8sObjectNamesResponse{}, err
	}

	securityContext, err := security.NewSecurityContext(request.TenantId, request.UserId)
	if err != nil {
		return GetK8sObjectNamesResponse{}, err
	}

	if !IsFeatureEnabled(nil, request.TenantId, FEATURE_RBACK_K8S_ACCESS) {
		return GetK8sObjectNamesResponse{
			Enabled: false,
		}, nil
	}

	names, err := securityContext.ListK8sObjectNames(request.AccountId, request.K8sObjectType, request.K8sPermission)
	if err != nil {
		return GetK8sObjectNamesResponse{
			Enabled: true,
		}, err
	}

	return GetK8sObjectNamesResponse{
		Enabled:     true,
		ObjectNames: names,
	}, nil
}

func GetSecurityContext(request GetSecurityContextRequest) (GetSecurityContextResponse, error) {
	err := common.ValidateStruct(request)
	if err != nil {
		return GetSecurityContextResponse{}, err
	}

	securityContext, err := security.NewSecurityContext(request.TenantId, request.UserId)
	if err != nil {
		return GetSecurityContextResponse{}, err
	}

	return GetSecurityContextResponse{
		Context: securityContext,
	}, nil
}
