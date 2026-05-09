import { SAML } from '@node-saml/passport-saml';
import { DOMParser } from '@xmldom/xmldom';
import { X509Certificate } from 'crypto';

export interface SamlConfig {
  enabled: boolean;
  entryPoint: string;
  issuer: string;
  callbackUrl: string;
  cert: string; // PEM formatted
  audience?: string;
}

export interface SamlUser {
  id: string;
  email: string;
  name?: string;
  nameID?: string;
  nameIDFormat?: string;
  sessionIndex?: string;
  groups?: string[]; // Groups/roles from SAML assertion
}

function ensurePem(cert?: string | null): string {
  if (!cert) {
    throw new Error('Certificate is required');
  }
  const clean = cert
    .replace(/-----BEGIN CERTIFICATE-----/g, '')
    .replace(/-----END CERTIFICATE-----/g, '')
    .replace(/\s+/g, '');
  return `-----BEGIN CERTIFICATE-----\n${clean}\n-----END CERTIFICATE-----`;
}

export function getSamlConfigFromEnv(): SamlConfig | null {
  const enabled = process.env.SAML_ENABLED === 'true';
  if (!enabled) {
    console.info('[SAML:Config] SAML is disabled (SAML_ENABLED != true)');
    return null;
  }

  const entryPoint = process.env.SAML_ENTRY_POINT;
  const issuer = process.env.SAML_ISSUER;
  const rawCert = process.env.SAML_CERT;
  const callbackBase = process.env.NEXTAUTH_URL;
  if (!entryPoint || !issuer || !rawCert || !callbackBase) {
    const missing = [!entryPoint && 'SAML_ENTRY_POINT', !issuer && 'SAML_ISSUER', !rawCert && 'SAML_CERT', !callbackBase && 'NEXTAUTH_URL'].filter(
      Boolean
    );
    console.error(`[SAML:Config] SAML enabled but missing required env vars: ${missing.join(', ')}`);
    return null;
  }
  return {
    enabled: true,
    entryPoint,
    issuer,
    callbackUrl: `${callbackBase.replace(/\/$/, '')}/api/auth/saml/acs`,
    cert: ensurePem(rawCert),
    audience: process.env.SAML_AUDIENCE || issuer,
  };
}

export function getCertificateExpiry(cert: string): Date {
  try {
    const pem = ensurePem(cert);
    const x509 = new X509Certificate(pem);
    return new Date(x509.validTo);
  } catch (err) {
    throw new Error(`Invalid certificate: ${(err as Error).message}`);
  }
}

export function checkCertificateStatus(cert: string) {
  const expiresAt = getCertificateExpiry(cert);
  const now = new Date();
  const days = Math.floor((expiresAt.getTime() - now.getTime()) / (1000 * 60 * 60 * 24));
  return {
    expired: expiresAt <= now,
    expiringSoon: days >= 0 && days < 30,
    expiresAt,
    daysUntilExpiry: days,
  };
}

function extractGroups(profile: any, email: string): string[] {
  const groupsRaw =
    profile.groups ||
    profile.Group ||
    profile.memberOf ||
    profile.roles ||
    profile.Role ||
    profile['http://schemas.xmlsoap.org/claims/Group'] ||
    profile['http://schemas.microsoft.com/ws/2008/06/identity/claims/groups'];

  if (!groupsRaw) {
    console.info(`[SAML:ACS] No groups found in SAML assertion for ${email}`);
    return [];
  }

  let groups: string[];
  if (Array.isArray(groupsRaw)) {
    groups = groupsRaw.map((g) => g.toString());
  } else if (typeof groupsRaw === 'string') {
    groups = [groupsRaw];
  } else {
    groups = [];
  }
  console.info(`[SAML:ACS] Groups found in assertion for ${email}: ${JSON.stringify(groups)}`);
  return groups;
}

function extractDisplayName(profile: any): string | undefined {
  if (typeof profile.displayName === 'string' && profile.displayName) {
    return profile.displayName;
  }
  if (typeof profile.name === 'string' && profile.name) {
    return profile.name;
  }
  if (typeof profile.firstName === 'string' && typeof profile.lastName === 'string') {
    return `${profile.firstName} ${profile.lastName}`;
  }
  return undefined;
}

export class SamlService {
  private readonly saml: SAML;
  private readonly samlAssertionOnly: SAML;
  private readonly samlResponseOnly: SAML;

  constructor(config: SamlConfig) {
    const baseOptions = {
      entryPoint: config.entryPoint,
      issuer: config.issuer,
      callbackUrl: config.callbackUrl,
      idpCert: config.cert,
      audience: config.audience,
      signatureAlgorithm: 'sha256' as const,
      identifierFormat: 'urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress',
      acceptedClockSkewMs: 5_000,
    };

    // Strict: require both response + assertion signatures
    this.saml = new SAML({ ...baseOptions, wantAuthnResponseSigned: true, wantAssertionsSigned: true });
    // Fallback A: assertion signed, response not (e.g. some Azure AD / Okta configs)
    this.samlAssertionOnly = new SAML({ ...baseOptions, wantAuthnResponseSigned: false, wantAssertionsSigned: true });
    // Fallback B: response signed (covers assertion), assertion not separately signed (e.g. Azure AD "Sign SAML response")
    this.samlResponseOnly = new SAML({ ...baseOptions, wantAuthnResponseSigned: true, wantAssertionsSigned: false });
  }

  async getAuthorizeUrl(host?: string): Promise<string> {
    console.info(`[SAML:Login] Generating authorize URL — host=${host || 'N/A'}`);
    const url = await this.saml.getAuthorizeUrlAsync('', host, {});
    console.info(`[SAML:Login] Redirect URL generated successfully`);
    return url;
  }

  async validatePostResponse(body: any) {
    console.info('[SAML:ACS] Validating SAML response...');
    const profile = await this.validateAndGetProfile(body);

    console.info(`[SAML:ACS] SAML response validated — nameID=${profile.nameID}, issuer=${profile.issuer}`);
    console.info('[SAML:ACS] SAML profile attributes:', JSON.stringify(profile, null, 2));

    const email = profile.email || profile.mail || profile.nameID;
    if (!email) {
      console.error('[SAML:ACS] No email found in SAML profile — checked: email, mail, nameID');
      throw new Error('Email missing in SAML profile');
    }

    const groups = extractGroups(profile, email);
    const name = extractDisplayName(profile);

    const user: SamlUser = {
      id: profile.nameID || email,
      email,
      name,
      nameID: profile.nameID,
      nameIDFormat: profile.nameIDFormat,
      sessionIndex: profile.sessionIndex,
      groups: groups.length > 0 ? groups : undefined,
    };
    console.info(
      `[SAML:ACS] Extracted user — email=${email}, name=${name || 'N/A'}, nameID=${profile.nameID}, groups=${
        groups.length > 0 ? JSON.stringify(groups) : 'none'
      }`
    );
    return { user, raw: profile };
  }

  /**
   * Check if the SAML Response element itself carries a ds:Signature.
   * Returns false when only the inner Assertion is signed (e.g. Azure AD).
   */
  private static responseHasSignature(body: any): boolean {
    const DSIG_NS = 'http://www.w3.org/2000/09/xmldsig#';
    try {
      const xml = Buffer.from(body.SAMLResponse, 'base64').toString('utf-8');
      const doc = new DOMParser().parseFromString(xml, 'text/xml');
      const response = doc.documentElement;
      if (!response) return true;
      // Check direct children of <Response> for a Signature element (any prefix)
      for (const child of Array.from(response.childNodes)) {
        if ((child as Element).localName === 'Signature' && (child as Element).namespaceURI === DSIG_NS) {
          return true;
        }
      }
      return false;
    } catch {
      return true; // On parse failure, assume signature is present → don't fall back
    }
  }

  private async validateAndGetProfile(body: any) {
    // Try strict validation first (response + assertion signed)
    try {
      const result = await this.saml.validatePostResponseAsync(body);
      if (result.profile) {
        return result.profile;
      }
    } catch (strictError: any) {
      // Case 1: Response has no signature, only assertion is signed (e.g. Azure AD "Sign SAML assertion")
      if (strictError.message === 'Invalid document signature' && !SamlService.responseHasSignature(body)) {
        console.warn('[SAML:ACS] No response-level ds:Signature found — retrying with assertion-only validation');
        try {
          const result = await this.samlAssertionOnly.validatePostResponseAsync(body);
          if (result.profile) {
            console.info('[SAML:ACS] Assertion-only signature validation succeeded');
            return result.profile;
          }
        } catch (fallbackError: any) {
          console.error(`[SAML:ACS] Assertion-only validation also FAILED — error=${fallbackError.message}`);
          throw fallbackError;
        }
      }

      // Case 2: Response is signed (covers assertion) but assertion has no separate signature (e.g. Azure AD "Sign SAML response")
      if (strictError.message === 'Invalid signature' && SamlService.responseHasSignature(body)) {
        console.warn('[SAML:ACS] Response signed but assertion not separately signed — retrying with response-only validation');
        try {
          const result = await this.samlResponseOnly.validatePostResponseAsync(body);
          if (result.profile) {
            console.info('[SAML:ACS] Response-only signature validation succeeded');
            return result.profile;
          }
        } catch (fallbackError: any) {
          console.error(`[SAML:ACS] Response-only validation also FAILED — error=${fallbackError.message}`);
          throw fallbackError;
        }
      }

      console.error(`[SAML:ACS] SAML response validation FAILED — error=${strictError.message}`);
      throw strictError;
    }
    console.error('[SAML:ACS] SAML response validated but no profile returned');
    throw new Error('No profile in SAML response');
  }
}

/**
 * Maps SAML groups to Nudgebee groups based on configuration
 *
 * Configuration via environment variable SAML_GROUP_MAPPING:
 * JSON object mapping SAML group names to Nudgebee group names
 *
 * Example:
 * SAML_GROUP_MAPPING={"okta-admins":"admins","okta-developers":"developers","okta-viewers":"viewers"}
 *
 * @param samlGroups - Groups from SAML assertion
 * @returns Array of Nudgebee group names
 */
export function mapSamlGroupsToNudgebeeGroups(samlGroups: string[]): string[] {
  if (!samlGroups || samlGroups.length === 0) {
    console.info('[SAML:GroupMapping] No SAML groups received from IdP');
    return [];
  }

  console.info(`[SAML:GroupMapping] Input SAML groups: ${JSON.stringify(samlGroups)}`);

  // Get mapping configuration from environment
  const mappingConfig = process.env.SAML_GROUP_MAPPING;

  if (!mappingConfig) {
    console.info('[SAML:GroupMapping] SAML_GROUP_MAPPING not configured, defaulting to tenant_admin');
    return ['tenant_admin'];
  }

  try {
    const mapping: Record<string, string> = JSON.parse(mappingConfig);
    console.info(`[SAML:GroupMapping] Mapping config: ${JSON.stringify(mapping)}`);
    const nudgebeeGroups: string[] = [];

    for (const samlGroup of samlGroups) {
      const mappedGroup = mapping[samlGroup];
      if (mappedGroup) {
        console.info(`[SAML:GroupMapping] Mapped "${samlGroup}" -> "${mappedGroup}"`);
        nudgebeeGroups.push(mappedGroup);
      } else {
        const includeUnmapped = process.env.SAML_INCLUDE_UNMAPPED_GROUPS === 'true';
        if (includeUnmapped) {
          console.info(`[SAML:GroupMapping] Unmapped group "${samlGroup}" included as-is (SAML_INCLUDE_UNMAPPED_GROUPS=true)`);
          nudgebeeGroups.push(samlGroup);
        } else {
          console.warn(`[SAML:GroupMapping] Group "${samlGroup}" has no mapping — skipped`);
        }
      }
    }

    console.info(`[SAML:GroupMapping] Final Nudgebee roles: ${JSON.stringify(nudgebeeGroups)}`);
    return nudgebeeGroups;
  } catch (error) {
    console.error('[SAML:GroupMapping] Failed to parse SAML_GROUP_MAPPING — falling back to raw groups:', error);
    return samlGroups;
  }
}
