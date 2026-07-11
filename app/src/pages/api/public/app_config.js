import loadBrandingFile from '@lib/loadBrandingFile';

export default function handler(req, res) {
  // Load branding from file if TENANT_BRANDING_FILE is set (e.g. "branding/xyz/theme.json")
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

  const isWhiteLabel = !!process.env.TENANT_BRANDING_FILE && process.env.TENANT_BRANDING_FILE !== 'branding/default/theme.json';

  res.status(200).json({
    isWhiteLabel,
    logoUrl: brandingFile?.logoUrl || '/branding/default/logo.svg',
    faviconUrl: brandingFile?.faviconUrl || '/favicon.ico',
    title: brandingFile?.title || 'Nudgebee',
    assistantName: brandingFile?.assistantName || 'nubi',
    nubiIconUrl: brandingFile?.nubiIconUrl || '/branding/default/nubi-icon.svg',
    nubiIconLightUrl: brandingFile?.nubiIconLightUrl || '/branding/default/nubi-icon-light.svg',
    signinImageUrl: brandingFile?.signinImageUrl ?? '',
    signinLeftImageUrl: brandingFile?.signinLeftImageUrl ?? '',
    // Optional partner-supplied auth carousel slides: [{ title, image }].
    // `image` is a URL (absolute or under /branding/<partner>/). Null ⇒ frontend uses bundled defaults.
    carouselSlides: Array.isArray(brandingFile?.carouselSlides) ? brandingFile.carouselSlides : null,
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
    // Optional tenant font remap: [{ family, src, weight?, style? }]. Re-points
    // hardcoded font-family names (e.g. Poppins/Roboto) at a brand font via
    // @font-face injected client-side. Null ⇒ no remap (default Roboto/Poppins).
    fontRemap: Array.isArray(brandingFile?.fontRemap) ? brandingFile.fontRemap : null,
  });
}
