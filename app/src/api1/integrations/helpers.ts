import { safeJSONParse } from 'src/utils/common';

/**
 * The integration list API returns `integrations_cloud_accounts` and
 * `integration_config_values` as JSON-encoded strings. Parse them once into
 * the array / object shapes the UI expects. A failed parse falls back to an
 * empty array so downstream code doesn't have to guard against malformed
 * responses.
 *
 * The same transform exists in app/src/pages/accounts/ListIntegrations.jsx
 * as a local helper — kept separate there because the standalone page predates
 * this module. Future cleanup could route that page through this helper too.
 */
export function parseIntegrationItem<T extends Record<string, unknown>>(item: T): T {
  return {
    ...item,
    integrations_cloud_accounts: safeJSONParse(item?.integrations_cloud_accounts as string) || [],
    integration_config_values: safeJSONParse(item?.integration_config_values as string) || [],
  };
}

/**
 * Mask string used in form inputs for encrypted fields (API keys, AWS
 * credentials, etc.). The backend never returns plaintext for these — the
 * form displays the mask, and on save / test:
 *   - if the user kept the mask → the original encrypted value is sent back
 *     with `is_encrypted: true` so the backend leaves it untouched.
 *   - if the user typed something new → the plaintext goes with
 *     `is_encrypted: false` and the backend re-encrypts.
 *
 * Must match the literal mask used in IntegrationDynamicFormModal — they
 * have to be byte-identical for the comparison logic to work.
 */
export const ENCRYPTED_MASK = '*************************************************';
