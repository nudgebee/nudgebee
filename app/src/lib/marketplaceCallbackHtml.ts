import loadBrandingFile from '@lib/loadBrandingFile';

// Marketplace callback pages render standalone (outside the React tree),
// so CSS variables from useThemeProvider are not available. We inline the
// tenant's colorTokens into a <style> block so var() references resolve and
// the page picks up partner branding from theme.json.
function brandingStyleBlock(): string {
  const tokens = loadBrandingFile()?.colorTokens || {};
  const decls = Object.entries(tokens)
    .map(([k, v]) => `${k}: ${v};`)
    .join(' ');
  return `<style>:root { ${decls} }</style>`;
}

const baseUrl = (): string => process.env.BASE_URL || '';
const title = (): string => process.env.DEFAULT_TITLE || '';

export function getAccountExistsHtml(): string {
  return `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Success - ${title()}</title>
    ${brandingStyleBlock()}
</head>
<body style="padding: 20px; font-family: var(--nb-font-primary, sans-serif); text-align: center; background-color: var(--nb-bg-pages, #f4f4f4); margin: 0;">
    <div style="background-color: var(--nb-bg-white, #fff); padding: 20px; border-radius: 5px; box-shadow: var(--nb-shadow-card, 0 2px 10px rgba(0, 0, 0, 0.1)); display: inline-block; margin: 0 auto; text-align: center;">
        <h2 style="color: var(--nb-text-title, #333); font-size: 24px;">Welcome!</h2>
        <p style="color: var(--nb-text-tertiary, #555); font-size: 16px;">Your tenant already exists, Click the button below to continue.</p>
        <a href="${baseUrl()}" style="display: inline-block; padding: 10px 20px; margin-top: 20px; font-size: 16px; color: var(--nb-btn-primary-text, #fff); background-color: var(--nb-bg-brand-button, #3470e9); border: none; border-radius: 5px; text-decoration: none; cursor: pointer;">Continue</a>
    </div>
</body>
</html>
`;
}

export function getErrorHtml(): string {
  return `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Failed - ${title()}</title>
    ${brandingStyleBlock()}
</head>
<body style="padding: 20px; font-family: var(--nb-font-primary, sans-serif); text-align: center; background-color: var(--nb-bg-pages, #f4f4f4); margin: 0;">
    <div style="background-color: var(--nb-bg-white, #fff); padding: 20px; border-radius: 5px; box-shadow: var(--nb-shadow-card, 0 2px 10px rgba(0, 0, 0, 0.1)); display: inline-block; margin: 0 auto; text-align: center;">
        <h2 style="color: var(--nb-text-error, #e60013); font-size: 24px;">Failed!</h2>
        <p style="color: var(--nb-text-tertiary, #555); font-size: 16px;">Unfortunately, there was an error while checking your details.</p>
        <p style="color: var(--nb-text-tertiary, #555); font-size: 16px;">Please try again or contact support.</p>
        <a href="${baseUrl()}" style="display: inline-block; padding: 10px 20px; margin-top: 20px; font-size: 16px; color: var(--nb-text-white, #fff); background-color: var(--nb-bg-error, #e60013); border: none; border-radius: 5px; text-decoration: none; cursor: pointer;">Go Back</a>
    </div>
</body>
</html>
`;
}
