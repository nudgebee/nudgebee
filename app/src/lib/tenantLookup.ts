import { getTenantAttributes } from '@lib/UserService';

/**
 * Find tenant by domain matching against tenant `allowed_domains` attribute.
 * Used by multi-tenant onboarding and SAML user creation.
 */
export async function findTenantByDomain(domain: string): Promise<{ tenantId: string; tenantAttrs: any[] }> {
  if (!domain) {
    return { tenantId: '', tenantAttrs: [] };
  }

  const tenantAttrs = await getTenantAttributes();
  if (!tenantAttrs?.length) {
    return { tenantId: '', tenantAttrs: [] };
  }

  const allowedDomainsArr = tenantAttrs.filter((f: any) => f.name === 'allowed_domains');

  for (const allowedDomains of allowedDomainsArr) {
    if (!allowedDomains.value) {
      continue;
    }

    try {
      const allowedDomainsList = JSON.parse(allowedDomains.value);
      if (Array.isArray(allowedDomainsList) && allowedDomainsList.includes(domain)) {
        return { tenantId: allowedDomains.tenant_id, tenantAttrs };
      }
    } catch (e) {
      console.log('Failed to parse allowedDomain -', e, allowedDomains);
    }
  }

  return { tenantId: '', tenantAttrs };
}
