/**
 * Resolves the one address the dummy-credentials provider may sign in as, or
 * '' when the deployment names none.
 *
 * The provider authenticates on a single shared password, so without this any
 * email gets in — an existing user is impersonated, and a new one is onboarded
 * as tenant_admin by the bootstrap gate. When the deployment names its admin
 * (the Helm chart's admin.email, surfaced as ADMIN_EMAIL), the provider is
 * pinned to that address.
 *
 * A licence address wins over ADMIN_EMAIL, mirroring adminEmail() in
 * api-server/services/bootstrap/provision.go: on a licensed deployment the
 * admin is provisioned from the licence and a conflicting admin.email is
 * ignored, so pinning to ADMIN_EMAIL there would lock the real admin out.
 *
 * '' keeps the historical any-email behavior for deployments that set neither
 * (local development, docker-compose, installs predating admin.email).
 */
export function dummyCredsAdminEmail(licenceEmail?: string, configuredEmail: string | undefined = process.env.ADMIN_EMAIL): string {
  return (licenceEmail?.trim() || configuredEmail?.trim() || '').toLowerCase();
}
