import type { NextApiRequest, NextApiResponse } from 'next';
import { v4 as uuidv4 } from 'uuid';
import { URLSearchParams } from 'url';
import { getAccountExistsHtml, getErrorHtml } from '@lib/marketplaceCallbackHtml';

const servicesEndpoint = process.env.SERVICE_API_SERVER_URL ?? 'http://localhost:8000';
const servicesServerToken = process.env.ACTION_API_SERVER_TOKEN ?? '';
const apiVersion = '2018-08-31';
const clientId = process.env.MS_TEAMS_CLIENT_ID ?? '';
const clientSecret = process.env.MS_TEAMS_CLIENT_SECRET ?? '';
const azureTenantId = process.env.AZURE_TENANT_ID ?? 'a3f24626-a242-4cc7-b3fe-4e2e76e805b6';

const getHtmlForGuest = (url: string, customerIdentifier: string): string => {
  return `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Nudgebee - Welcome</title>
</head>
<body style="padding: 20px; font-family: Verdana, sans-serif; text-align: center; background-color: #f4f4f4; margin: 0;">
    <div style="background-color: #fff; padding: 20px; border-radius: 5px; box-shadow: 0 2px 10px rgba(0, 0, 0, 0.1); display: inline-block; margin: 0 auto; text-align: left;">
        <h1 style="color: #333; font-size: 28px;">Hello!</h1>
        <p style="color: #555; font-size: 16px; margin-bottom: 20px;">Welcome to Nudgebee. Please enter your details below:</p>
        <p style="color: #555; font-size: 14px; margin-bottom: 20px;">This will help us create a tenant for you.</p>
        <form id="tenant-form" style="display: flex; flex-direction: column;" action="${url}" method="POST">
            <label for="first-name" style="font-size: 16px; color: #333; margin-bottom: 5px;">First Name:</label>
            <input type="text" id="firstname" name="firstname" required style="padding: 10px; font-size: 16px; margin-bottom: 15px; border: 1px solid #ccc; border-radius: 5px; width: 95%;">

            <label for="last-name" style="font-size: 16px; color: #333; margin-bottom: 5px;">Last Name:</label>
            <input type="text" id="lastname" name="lastname" required style="padding: 10px; font-size: 16px; margin-bottom: 15px; border: 1px solid #ccc; border-radius: 5px; width: 95%;">

            <label for="email" style="font-size: 16px; color: #333; margin-bottom: 5px;">Email ID:</label>
            <input type="email" id="email" name="email" required style="padding: 10px; font-size: 16px; margin-bottom: 15px; border: 1px solid #ccc; border-radius: 5px; width: 95%;">

            <label style="font-size: 16px; color: #333; margin-bottom: 10px; display: block;">Offering Type:</label>
            <div style="margin-bottom: 15px;">
                <label style="font-size: 14px; color: #333; margin-right: 20px; display: inline-flex; align-items: center;">
                    <input type="radio" id="saas" name="offeringtype" value="saas" checked style="margin-right: 8px;">
                    SaaS
                </label>
                <label style="font-size: 14px; color: #333; display: inline-flex; align-items: center;">
                    <input type="radio" id="onprem" name="offeringtype" value="onprem" style="margin-right: 8px;">
                    On-Prem
                </label>
            </div>

            <button type="submit" style="padding: 10px 20px; font-size: 16px; color: #fff; background-color: #FACF39; border: none; border-radius: 5px; cursor: pointer; text-align: center; margin-top: 10px;">Submit</button>

            <input type="hidden" name="customeridentifier" value="${customerIdentifier}">
        </form>
    </div>
</body>
</html>
  `;
};

async function getAuthToken() {
  const tokenEndpoint = `https://login.microsoftonline.com/${azureTenantId}/oauth2/v2.0/token`;
  const data = new URLSearchParams({
    client_id: clientId,
    client_secret: clientSecret,
    scope: '20e940b3-4c77-4b0b-9a53-9e16a1b010a7/.default',
    grant_type: 'client_credentials',
  }).toString();

  try {
    const response = await fetch(tokenEndpoint, {
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: data,
      method: 'POST',
    });
    const res = await response.json();
    if (res.error) {
      console.error('error getting auth token, ' + res.error_description);
    }
    return res.access_token || '';
  } catch (error) {
    console.log(error);
    return '';
  }
}

function getRequestId(req: NextApiRequest): string {
  const requestIds = req.headers['x-request-id'];
  return Array.isArray(requestIds) ? requestIds[0] : requestIds ?? uuidv4();
}

async function createCustomerResponse(subJson: any) {
  const subscriptionPayload = {
    customer_identifier: subJson.id,
    provider_account_id: subJson.subscription.beneficiary.tenantId,
    product_code: subJson.subscription.offerId,
    marketplace: 'azure',
  };

  return await fetch(`${servicesEndpoint}/marketplace/subscribe`, {
    headers: {
      'Content-Type': 'application/json',
      'X-ACTION-TOKEN': servicesServerToken,
    },
    body: JSON.stringify(subscriptionPayload),
    method: 'POST',
  });
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const marketplaceToken = req.query['token'] as string | undefined;
  if (!marketplaceToken) {
    return res.status(400).send('Unable to get marketplace token!');
  }

  const accessToken = await getAuthToken();
  if (!accessToken) {
    return res.status(400).send('Unable to authenticate!');
  }

  const newTenantEndpoint = `${process.env.BASE_URL}/api/marketplace/new`;
  try {
    const subscriptionResponse = await fetch(`https://marketplaceapi.microsoft.com/api/saas/subscriptions/resolve?api-version=${apiVersion}`, {
      headers: {
        Authorization: `Bearer ${accessToken}`,
        'x-ms-marketplace-token': marketplaceToken,
      },
      method: 'POST',
    });

    if (!subscriptionResponse.ok) {
      return res.setHeader('Content-Type', 'text/html').status(200).send(getErrorHtml());
    }

    const subJson = await subscriptionResponse.json();
    const tenantCreationRequest = await createCustomerResponse(subJson);

    if (!tenantCreationRequest.ok) {
      return res.setHeader('Content-Type', 'text/html').status(200).send(getErrorHtml());
    }

    const responseJson = await tenantCreationRequest.json();

    if (!responseJson.tenant_id) {
      const activateResponse = await fetch(
        `https://marketplaceapi.microsoft.com/api/saas/subscriptions/${subJson.id}/activate?api-version=${apiVersion}`,
        {
          headers: {
            Authorization: `Bearer ${accessToken}`,
          },
          method: 'POST',
        }
      );

      if (!activateResponse.ok) {
        return res.setHeader('Content-Type', 'text/html').status(200).send(getErrorHtml());
      }

      return res
        .setHeader('Content-Type', 'text/html')
        .setHeader('x-request-id', getRequestId(req))
        .status(200)
        .send(getHtmlForGuest(newTenantEndpoint, subJson.id));
    }

    return res.setHeader('Content-Type', 'text/html').status(200).send(getAccountExistsHtml());
  } catch (err) {
    console.error(err);
    return res.setHeader('Content-Type', 'text/html').status(200).send(getErrorHtml());
  }
}
