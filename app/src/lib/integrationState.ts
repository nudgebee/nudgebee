import { decrypt, encrypt } from '@lib/internal';

// Identity carried through an OAuth round-trip in the provider's `state` param, so the
// callback recovers it by decryption instead of re-reading the session cookie — which a
// cross-site redirect back from the provider can drop (SameSite / on-prem host drift).
export type IntegrationIdentity = {
  tenant_id: string;
  user_email: string;
};

const MAX_STATE_AGE_MS = 15 * 60 * 1000; // replay bound
const MAX_CLOCK_SKEW_MS = 5 * 60 * 1000; // reject implausibly future-dated state

export async function encodeIdentityState(identity: IntegrationIdentity): Promise<string> {
  return encrypt(JSON.stringify({ ...identity, ts: Date.now() }));
}

// Null for absent / legacy-uuid4 / tampered / expired state — callers then fall back to the cookie.
export async function decodeIdentityState(state: string | string[] | undefined): Promise<IntegrationIdentity | null> {
  const raw = Array.isArray(state) ? state[0] : state;
  if (typeof raw !== 'string' || raw.length === 0) return null;
  try {
    const parsed = JSON.parse(await decrypt(raw)) as { tenant_id?: unknown; user_email?: unknown; ts?: unknown };
    if (typeof parsed.tenant_id !== 'string' || parsed.tenant_id.length === 0) return null;
    if (typeof parsed.ts !== 'number') return null;
    const age = Date.now() - parsed.ts;
    if (age > MAX_STATE_AGE_MS || age < -MAX_CLOCK_SKEW_MS) return null;
    return {
      tenant_id: parsed.tenant_id,
      user_email: typeof parsed.user_email === 'string' && parsed.user_email.length > 0 ? parsed.user_email : 'system',
    };
  } catch {
    return null;
  }
}
