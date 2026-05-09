import * as crypto from 'crypto';
import * as jose from 'jose';
import * as bcrypt from 'bcrypt';

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

//openssl genpkey -out rsakey.pem -algorithm RSA -pkeyopt rsa_keygen_bits:2048
//openssl pkey -in rsakey.pem -pubout -out rsapubkey.pem
const JWT_PRIVATE_KEY_DATA = process.env.NEXTAUTH_PRIVATE_KEY?.replace(/\\n/g, '\n') || '';

export const JWT_PRIVATE_KEY = jose.importPKCS8(JWT_PRIVATE_KEY_DATA, 'RS256');

export async function encodeHasuraJWT(user: any, claims: any, exp: number, iat: number): Promise<string> {
  const jwtPrivateKey = await JWT_PRIVATE_KEY;
  const roles = user?.roles ?? [];
  if (user) {
    //marker role, for sceanrio where user has no roles
    let defaultRole = 'tenant_usage';
    if (roles.length === 0) {
      roles.push('tenant_usage');
    }

    if (roles.includes('tenant_admin')) {
      defaultRole = 'tenant_admin';
    } else if (roles.includes('tenant_admin_readonly')) {
      defaultRole = 'tenant_admin_readonly';
    } else if (roles.includes('account_admin')) {
      defaultRole = 'account_admin';
    } else if (roles.includes('account_admin_readonly')) {
      defaultRole = 'account_admin_readonly';
    } else if (roles.includes('k8s_namespace_admin')) {
      defaultRole = 'k8s_namespace_admin';
    } else if (roles.includes('k8s_namespace_admin_readonly')) {
      defaultRole = 'k8s_namespace_admin_readonly';
    }

    claims['https://hasura.io/jwt/claims'] = JSON.stringify({
      'x-hasura-default-role': defaultRole,
      'x-hasura-user-id': user?.id,
      'x-hasura-user-tenant-id': user?.tenant?.id || user?.tenant?.tenant,
      'x-hasura-user-account-ids': `{${user?.accountIds?.join(',') ?? ''}}`,
      'x-hasura-user-readonly-account-ids': `{${user?.readOnlyAccountIds?.join(',') ?? ''}}`,
      'x-hasura-user-namespaced-account-ids': `{${user?.namespacedAccountIds?.join(',') ?? ''}}`,
      'x-hasura-user-namespaced-readonly-account-ids': `{${user?.namespacedReadOnlyAccountIds?.join(',') ?? ''}}`,
      'x-hasura-allowed-roles': roles,
    });
  }
  const jwt = await new jose.SignJWT(claims)
    .setProtectedHeader({ alg: 'RS256' })
    .setIssuedAt(iat)
    .setIssuer('urn:pollux:issuer')
    .setAudience('urn:pollux:hasura')
    .setExpirationTime(exp || '2h')
    .sign(jwtPrivateKey);

  return jwt;
}

export async function decodeHasuraJWT(token: string): Promise<jose.JWTVerifyResult> {
  const jwtPrivateKey = await JWT_PRIVATE_KEY;
  return await jose.jwtVerify(token, jwtPrivateKey, { algorithms: ['RS256'] });
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
