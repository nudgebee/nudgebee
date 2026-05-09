import loadBrandingFile from '@lib/loadBrandingFile';

export default function handler(req, res) {
  // Load branding from file if TENANT_BRANDING_FILE is set (e.g. "branding/rackspace/theme.json")
  const brandingFile = loadBrandingFile();

  // Parse optional theme config from env vars (override file if both set)
  let theme = brandingFile?.theme || null;
  let colorTokens = brandingFile?.colorTokens || null;

  try {
    if (process.env.TENANT_THEME_CONFIG) {
      theme = JSON.parse(process.env.TENANT_THEME_CONFIG);
    }
  } catch {
    // Invalid JSON — ignore, use defaults
  }

  try {
    if (process.env.TENANT_COLOR_TOKENS) {
      colorTokens = JSON.parse(process.env.TENANT_COLOR_TOKENS);
    }
  } catch {
    // Invalid JSON — ignore, use defaults
  }

  res.status(200).json({
    logoUrl: brandingFile?.logoUrl || '/branding/default/logo.svg',
    faviconUrl: brandingFile?.faviconUrl || '/favicon.ico',
    title: brandingFile?.title || 'Nudgebee',
    assistantName: brandingFile?.assistantName || 'nubi',
    nubiIconUrl: brandingFile?.nubiIconUrl || '/branding/default/nubi-icon.svg',
    nubiIconLightUrl: brandingFile?.nubiIconLightUrl || '/branding/default/nubi-icon-light.svg',
    signinImageUrl: brandingFile?.signinImageUrl ?? '',
    signinLeftImageUrl: brandingFile?.signinLeftImageUrl ?? '',
    loaderUrl: brandingFile?.loaderUrl || '',
    helpbeeIconUrl: brandingFile?.helpbeeIconUrl || '',
    troubleshootBeeUrl: brandingFile?.troubleshootBeeUrl || '',
    optimizeBeeUrl: brandingFile?.optimizeBeeUrl || '',
    k8sBeeUrl: brandingFile?.k8sBeeUrl || '',
    newUserBeeUrl: brandingFile?.newUserBeeUrl || '',
    relayUrl: process.env.RELAY_WSSERVER_ENDPOINT || '',
    k8sCollectorUrl: process.env.K8S_COLLECTOR_ENDPOINT || '',
    signingPublicKey: process.env.SIGNING_PUBLIC_KEY || '',
    theme,
    colorTokens,
  });
}
