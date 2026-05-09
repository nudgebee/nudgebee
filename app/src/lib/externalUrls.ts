/**
 * Centralized configuration for external URLs and registries.
 * Override per-deployment via the documented NEXT_PUBLIC_* env vars.
 */

const trimTrailingSlash = (s: string) => s.replace(/\/+$/, '');

/**
 * Base URL for the public documentation site, without trailing slash.
 * Override with NEXT_PUBLIC_DOCS_URL.
 */
export const DOCS_BASE_URL = trimTrailingSlash(process.env.NEXT_PUBLIC_DOCS_URL || 'https://docs.nudgebee.com');

/**
 * Build a full docs URL by joining DOCS_BASE_URL with the given path.
 * The path may begin with or without a leading slash.
 */
export function docsUrl(path: string): string {
  const suffix = path.startsWith('/') ? path : `/${path}`;
  return `${DOCS_BASE_URL}${suffix}`;
}

/**
 * Default Docker image registry that the K8s installer points to when the
 * operator hasn't entered a custom registry. Override with
 * NEXT_PUBLIC_DEFAULT_IMAGE_REGISTRY.
 */
export const DEFAULT_IMAGE_REGISTRY = process.env.NEXT_PUBLIC_DEFAULT_IMAGE_REGISTRY;

/**
 * Resolve the deployment's own origin (used for OAuth redirect URIs,
 * outbound webhook callback URLs, and curl-example snippets).
 *
 * On the client, prefers `window.location` so the URL always matches the
 * domain the user actually loaded the app from. On the server, falls
 * back through the documented env vars and finally to a localhost dev
 * default that's safe for OSS / dev deployments.
 */
export function getAppBaseUrl(): string {
  if (typeof window !== 'undefined') {
    return `${window.location.protocol}//${window.location.host}`;
  }
  return process.env.BASE_URL || process.env.NEXT_PUBLIC_APP_URL || 'http://localhost:3000';
}
