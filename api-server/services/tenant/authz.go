package tenant

// eventually move to security package
// currently its here because of circular dependency caused by feature flag apis

import (
	"log/slog"
	"nudgebee/services/common"
	"nudgebee/services/security"
)

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
		allowed := false

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

		if securityContext.IsTenantAdmin() {
			allowed = true
		} else if securityContext.IsTenantReadAdmin() && access.Permission == security.SecurityAccessTypeRead {
			allowed = true
		} else if access.Args.AccountId != "" && securityContext.HasAccountAccess(access.Args.AccountId, access.Permission) {
			allowed = true
		}
		if IsFeatureEnabled(security.NewRequestContextForSuperAdmin(slog.Default(), nil, nil), access.TenantId, FEATURE_RBACK_K8S_ACCESS) && access.Args.K8sObjectName != "" && access.Args.K8sObjectType != "" {
			allowed, err := securityContext.HasK8sAccess(access.Args.AccountId, access.Args.K8sObjectType, access.Args.K8sObjectName, security.K8sRbacPermissionType(string(access.Permission)))
			if err != nil {
				response.Access = append(response.Access, ValidateAccessResponseAccess{
					Allowed: false,
					Message: err.Error(),
				})
				continue
			} else {
				response.Access = append(response.Access, ValidateAccessResponseAccess{
					Allowed: allowed,
					Message: "",
				})
			}
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
