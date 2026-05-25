import * as crypto from 'crypto';
import * as jose from 'jose';
import * as bcrypt from 'bcrypt';
import { pickDefaultRole } from '@lib/rolePriority';

const NUDGEBEE_ENCRYPTION_KEY_HEX = process.env.NUDGEBEE_ENCRYPTION_KEY || '';
let ENCRYPTION_KEY: Buffer;

try {
  ENCRYPTION_KEY = Buffer.from(NUDGEBEE_ENCRYPTION_KEY_HEX, 'hex');
  if (ENCRYPTION_KEY.length !== 32) {
    throw new Error('NUDGEBEE_ENCRYPTION_KEY must be a 64-character hex string (32 bytes for AES-256).');
  }
} catch (e: any) {
  console.error('Encryption key error:', e.message);
  throw new Error('Invalid NUDGEBEE_ENCRYPTION_KEY provided. Please ensure it is a 64-character hex string.');
}

const ENCRYPTION_ALGO = 'aes-256-gcm';

// Session JWT signing key. HMAC-SHA256 with NEXTAUTH_SECRET — the same
// secret NextAuth uses for its own session token, kept in a single env
// var. RSA/RS256 was historically required by Hasura to verify the JWT
// out-of-process; with Hasura gone, sign and verify both happen inside
// the same Next.js process so a symmetric MAC is sufficient (and the
// AES-GCM wrapper applied at the transport layer already provides
// authenticated encryption — the inner MAC is the second layer).
//
// Fail fast at module load if NEXTAUTH_SECRET is missing — silently
// signing tokens with an empty key would still produce verifiable JWTs
// against the same empty key, masking misconfiguration as success.
if (!process.env.NEXTAUTH_SECRET) {
  throw new Error('NEXTAUTH_SECRET environment variable is required for session JWT signing.');
}
const JWT_SECRET = new TextEncoder().encode(process.env.NEXTAUTH_SECRET);

export async function encodeSessionJWT(user: any, claims: any, exp: number, iat: number): Promise<string> {
  const roles = user?.roles ?? [];
  if (user) {
    //marker role, for sceanrio where user has no roles
    if (roles.length === 0) {
      roles.push('tenant_usage');
    }
    const defaultRole = pickDefaultRole(roles, 'tenant_usage');

    // Session claims at JWT root.
    claims.default_role = defaultRole;
    claims.user_id = user?.id;
    claims.tenant_id = user?.tenant?.id || user?.tenant?.tenant;
    claims.account_ids = user?.accountIds ?? [];
    claims.readonly_account_ids = user?.readOnlyAccountIds ?? [];
    claims.namespaced_account_ids = user?.namespacedAccountIds ?? [];
    claims.namespaced_readonly_account_ids = user?.namespacedReadOnlyAccountIds ?? [];
    claims.allowed_roles = roles;
  }
  const jwt = await new jose.SignJWT(claims)
    .setProtectedHeader({ alg: 'HS256' })
    .setIssuedAt(iat)
    .setIssuer('urn:nudgebee:issuer')
    .setAudience('urn:nudgebee:app')
    .setExpirationTime(exp || '2h')
    .sign(JWT_SECRET);

  return jwt;
}

export async function decodeSessionJWT(token: string): Promise<jose.JWTVerifyResult> {
  // Verify issuer + audience too, not just signature. Closes a theoretical
  // cross-token-reuse vector where a token signed by NEXTAUTH_SECRET for a
  // different purpose (e.g. a future API token) would otherwise be accepted
  // here. Both values must match what encodeSessionJWT() sets above.
  return await jose.jwtVerify(token, JWT_SECRET, {
    algorithms: ['HS256'],
    issuer: 'urn:nudgebee:issuer',
    audience: 'urn:nudgebee:app',
  });
}

export async function encrypt(message: string): Promise<string> {
  const iv = crypto.randomBytes(12); // GCM recommended IV size is 12 bytes
  const cipher = crypto.createCipheriv(ENCRYPTION_ALGO, ENCRYPTION_KEY, iv);

  const encrypted = Buffer.concat([cipher.update(message, 'utf8'), cipher.final()]);
  const authTag = cipher.getAuthTag();

  // Concatenate IV, encrypted data, and auth tag, then hex encode
  const fullCiphertext = Buffer.concat([iv, encrypted, authTag]);
  return fullCiphertext.toString('hex');
}

export async function decrypt(encryptedHex: string): Promise<string> {
  const fullCiphertext = Buffer.from(encryptedHex, 'hex');

  const iv = fullCiphertext.subarray(0, 12);
  const ciphertext = fullCiphertext.subarray(12, fullCiphertext.length - 16);
  const authTag = fullCiphertext.subarray(fullCiphertext.length - 16);

  const decipher = crypto.createDecipheriv(ENCRYPTION_ALGO, ENCRYPTION_KEY, iv);
  decipher.setAuthTag(authTag);

  const decrypted = Buffer.concat([decipher.update(ciphertext), decipher.final()]);
  return decrypted.toString('utf8');
}

// uses sha256 for hashing
export async function hashTextFast(data: string): Promise<string> {
  const hash = crypto.createHash('sha256');
  hash.update(data);
  return hash.digest('hex');
}

export async function hashPassword(password: string): Promise<string> {
  const saltRounds = 10;
  return bcrypt.hash(password, saltRounds);
}

export async function validateHashedPassword(password: string, hash: string): Promise<boolean> {
  return bcrypt.compare(password, hash);
}
