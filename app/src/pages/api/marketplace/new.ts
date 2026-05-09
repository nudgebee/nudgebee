import type { NextApiRequest, NextApiResponse } from 'next';
import { getErrorHtml } from '@utils/common';

const servicesEndpoint = process.env.SERVICE_API_SERVER_URL ?? 'http://localhost:8000';
const servicesServerToken = process.env.ACTION_API_SERVER_TOKEN ?? '';

// Conservative RFC-5322-ish email shape — local@domain.tld with at-least-2-char TLD.
// Validates the env-var input before splicing it into HTML to neutralize stray quotes,
// angle brackets, or other characters that would break the surrounding markup or open
// a stored-XSS path through an operator-controlled config value.
const EMAIL_REGEX = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;

/**
 * Render the "please contact <X> for your license" paragraph.
 *
 * Returns an empty string when MARKETPLACE_CONTACT_EMAIL is unset or fails
 * email validation — the calling templates already include a "Our team will
 * reach out to you with next steps" paragraph below this slot, which carries
 * the no-contact-email case on its own. Rendering a second "team will get in
 * touch" line here would be redundant.
 */
function getContactParagraph(): string {
  const contactEmail = process.env.MARKETPLACE_CONTACT_EMAIL?.trim();
  if (!contactEmail || !EMAIL_REGEX.test(contactEmail)) {
    return '';
  }
  return `<p style="color: #555; font-size: 16px; margin-bottom: 15px;">Please contact <a href="mailto:${contactEmail}" style="color: #007bff; text-decoration: none;">${contactEmail}</a> for your license.</p>`;
}

function getSuccessHtml(): string {
  return `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Success - Nudgebee</title>
</head>
<body style="padding: 20px; font-family: Verdana, sans-serif; text-align: center; background-color: #f4f4f4; margin: 0;">
    <div style="background-color: #fff; padding: 20px; border-radius: 5px; box-shadow: 0 2px 10px rgba(0, 0, 0, 0.1); display: inline-block; margin: 0 auto; text-align: center;">
        <h2 style="color: #333; font-size: 24px;">Welcome!</h2>
        <p style="color: #555; font-size: 16px;">Your tenant has been successfully created.</p>
        <p style="color: #555; font-size: 16px;">Click the button below to go to Nudgebee.</p>
        <a href="${process.env.BASE_URL}" style="display: inline-block; padding: 10px 20px; margin-top: 20px; font-size: 16px; color: #fff; background-color: #FACF39; border: none; border-radius: 5px; text-decoration: none; cursor: pointer;">Go to Nudgebee</a>
    </div>
</body>
</html>
`;
}

function getOnPremHtml(): string {
  return `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Thank You - Nudgebee</title>
</head>
<body style="padding: 20px; font-family: Verdana, sans-serif; text-align: center; background-color: #f4f4f4; margin: 0;">
    <div style="background-color: #fff; padding: 20px; border-radius: 5px; box-shadow: 0 2px 10px rgba(0, 0, 0, 0.1); display: inline-block; margin: 0 auto; text-align: center;">
        <h2 style="color: #333; font-size: 24px;">Thank You!</h2>
        <p style="color: #555; font-size: 16px; margin-bottom: 15px;">We appreciate your interest in our On-Premise solution.</p>
        ${getContactParagraph()}
        <p style="color: #777; font-size: 14px;">Our team will reach out to you with next steps for your On-Premise deployment.</p>
    </div>
</body>
</html>
`;
}

function getLicenseOnlyHtml(): string {
  return `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>License Request - Nudgebee</title>
</head>
<body style="padding: 20px; font-family: Verdana, sans-serif; text-align: center; background-color: #f4f4f4; margin: 0;">
    <div style="background-color: #fff; padding: 20px; border-radius: 5px; box-shadow: 0 2px 10px rgba(0, 0, 0, 0.1); display: inline-block; margin: 0 auto; text-align: center;">
        <h2 style="color: #333; font-size: 24px;">Thank You!</h2>
        <p style="color: #555; font-size: 16px; margin-bottom: 15px;">Your license request has been received.</p>
        ${getContactParagraph()}
        <p style="color: #777; font-size: 14px;">Our team will reach out to you with next steps.</p>
    </div>
</body>
</html>
`;
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const userRequest = req.body;

  if (userRequest.offeringtype === 'onprem') {
    return res.setHeader('Content-Type', 'text/html').status(200).send(getOnPremHtml());
  }

  // Handle license-only requests (no tenant creation)
  if (userRequest.onboardingtype === 'license') {
    return res.setHeader('Content-Type', 'text/html').status(200).send(getLicenseOnlyHtml());
  }

  // Use provided tenant name or fallback to customer identifier
  const tenantName = userRequest.tenantname?.trim() || userRequest.customeridentifier;

  try {
    const response = await fetch(servicesEndpoint + '/marketplace/create/tenant-user', {
      headers: {
        'Content-Type': 'application/json',
        'X-ACTION-TOKEN': servicesServerToken,
      },
      body: JSON.stringify({
        username: userRequest.email,
        firstname: userRequest.firstname,
        lastname: userRequest.lastname,
        tenantname: tenantName,
        customer_identifier: userRequest.customeridentifier,
        role: 'tenant_admin',
      }),
      method: 'post',
    });
    if (response.ok) {
      const responseJson = await response.json();

      console.log(responseJson);
      res.setHeader('Content-Type', 'text/html').status(200).send(getSuccessHtml());
    } else {
      res.setHeader('Content-Type', 'text/html').status(200).send(getErrorHtml);
    }
  } catch (err) {
    console.log(err);
    res.setHeader('Content-Type', 'text/html').status(200).send(getErrorHtml);
  }
}
