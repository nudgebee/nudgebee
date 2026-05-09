import { getSamlConfigFromEnv, SamlService } from './saml';

let _instance: SamlService | null = null;

export function getSamlService(): SamlService | null {
  if (_instance) {
    return _instance;
  }
  const cfg = getSamlConfigFromEnv();
  if (!cfg) {
    console.warn(
      '[SAML] Service not initialized — configuration is missing or incomplete. Check SAML_ENABLED, SAML_ENTRY_POINT, SAML_ISSUER, SAML_CERT, NEXTAUTH_URL'
    );
    return null;
  }
  console.info(
    `[SAML] Service initialized — entryPoint=${cfg.entryPoint}, issuer=${cfg.issuer}, callbackUrl=${cfg.callbackUrl}, audience=${
      cfg.audience || 'N/A'
    }`
  );
  _instance = new SamlService(cfg);
  return _instance;
}
