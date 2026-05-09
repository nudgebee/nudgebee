import type { NextApiRequest, NextApiResponse } from 'next';
import { checkCertificateStatus, getSamlConfigFromEnv } from '@lib/saml';

/**
 * SAML Health Check Endpoint
 *
 * Returns the health status of SAML configuration including certificate expiry information.
 * Useful for monitoring and alerting on certificate expiration.
 *
 * Response:
 * {
 *   enabled: boolean,
 *   status: 'healthy' | 'warning' | 'error',
 *   certificate: {
 *     expired: boolean,
 *     expiringSoon: boolean,
 *     expiresAt: string (ISO date),
 *     daysUntilExpiry: number
 *   },
 *   config: {
 *     entryPoint: string,
 *     issuer: string,
 *     callbackUrl: string
 *   }
 * }
 */
export default function handler(req: NextApiRequest, res: NextApiResponse) {
  if (req.method !== 'GET') {
    return res.status(405).json({ error: 'Method not allowed' });
  }

  try {
    const config = getSamlConfigFromEnv();

    if (!config) {
      return res.status(200).json({
        enabled: false,
        status: 'disabled',
        message: 'SAML authentication is not configured',
      });
    }

    // Check certificate status
    const certStatus = checkCertificateStatus(config.cert);

    // Determine overall health status
    let status: 'healthy' | 'warning' | 'error';
    if (certStatus.expired) {
      status = 'error';
    } else if (certStatus.expiringSoon) {
      status = 'warning';
    } else {
      status = 'healthy';
    }

    return res.status(200).json({
      enabled: true,
      status,
      certificate: {
        expired: certStatus.expired,
        expiringSoon: certStatus.expiringSoon,
        expiresAt: certStatus.expiresAt.toISOString(),
        daysUntilExpiry: certStatus.daysUntilExpiry,
      },
      config: {
        entryPoint: config.entryPoint,
        issuer: config.issuer,
        callbackUrl: config.callbackUrl,
      },
      messages: [
        certStatus.expired
          ? 'Certificate has EXPIRED! SAML authentication will fail.'
          : certStatus.expiringSoon
          ? `Certificate expires in ${certStatus.daysUntilExpiry} days. Please renew soon.`
          : `Certificate is valid for ${certStatus.daysUntilExpiry} more days.`,
      ],
    });
  } catch (error: any) {
    console.error('SAML health check error:', error);
    return res.status(500).json({
      enabled: false,
      status: 'error',
      error: 'Failed to check SAML health',
      message: error.message,
    });
  }
}
