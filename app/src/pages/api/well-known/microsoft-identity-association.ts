import type { NextApiRequest, NextApiResponse } from 'next';

/**
 * Returns the Microsoft Identity Association manifest used to verify
 * domain ownership for an Azure AD application.
 *
 * Set MICROSOFT_IDENTITY_APPLICATION_ID to the Azure AD application
 * (client) ID associated with this deployment's domain. If unset, the
 * endpoint returns an empty association — appropriate for deployments
 * that have not registered a domain with Microsoft.
 */
export default function handler(_req: NextApiRequest, res: NextApiResponse) {
  const applicationId = process.env.MICROSOFT_IDENTITY_APPLICATION_ID;

  res.setHeader('Content-Type', 'application/json');
  res.status(200).json({
    associatedApplications: applicationId ? [{ applicationId }] : [],
  });
}
