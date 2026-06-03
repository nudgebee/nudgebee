import type { NextApiRequest, NextApiResponse } from 'next';
import AWS from 'aws-sdk';
import { v4 as uuidv4 } from 'uuid';
import { getAccountExistsHtml, getErrorHtml } from '@lib/marketplaceCallbackHtml';

const servicesEndpoint = process.env.SERVICE_API_SERVER_URL ?? 'http://localhost:8000';
const servicesServerToken = process.env.ACTION_API_SERVER_TOKEN ?? '';

const getHtmlForGuest = (url: string, resolvedCustomerResponse: ResolveCustomerResponse): string => {
  return `
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Nudgebee - Welcome</title>
    <style>
        .form-field { margin-bottom: 15px; }
        .form-field label { font-size: 16px; color: #333; margin-bottom: 5px; display: block; }
        .form-field input { padding: 10px; font-size: 16px; border: 1px solid #ccc; border-radius: 5px; width: 95%; }
    </style>
</head>
<body style="padding: 20px; font-family: Verdana, sans-serif; text-align: center; background-color: #f4f4f4; margin: 0;">
    <div style="background-color: #fff; padding: 20px; border-radius: 5px; box-shadow: 0 2px 10px rgba(0, 0, 0, 0.1); display: inline-block; margin: 0 auto; text-align: left; min-width: 350px;">
        <h1 style="color: #333; font-size: 28px;">Hello!</h1>
        <p style="color: #555; font-size: 16px; margin-bottom: 20px;">Welcome to Nudgebee. Please fill in your details to create your account:</p>

        <form id="tenant-form" style="display: flex; flex-direction: column;" action="${url}" method="POST">
            <div class="form-field">
                <label for="tenantname">Tenant Name:</label>
                <input type="text" id="tenantname" name="tenantname" placeholder="Your organization name">
            </div>

            <div class="form-field">
                <label for="firstname">First Name:</label>
                <input type="text" id="firstname" name="firstname" required>
            </div>

            <div class="form-field">
                <label for="lastname">Last Name:</label>
                <input type="text" id="lastname" name="lastname" required>
            </div>

            <div class="form-field">
                <label for="email">Email ID:</label>
                <input type="email" id="email" name="email" required>
            </div>

            <button type="submit" style="padding: 10px 20px; font-size: 16px; color: #fff; background-color: #FACF39; border: none; border-radius: 5px; cursor: pointer; text-align: center; margin-top: 10px;">Submit</button>

            <input type="hidden" name="customeridentifier" value="${resolvedCustomerResponse.CustomerIdentifier}">
            <input type="hidden" name="onboardingtype" value="tenant">
        </form>
    </div>
</body>
</html>
  `;
};

const marketplaceMetering = new AWS.MarketplaceMetering({
  region: 'us-east-1',
  accessKeyId: process.env.AWS_SELLER_ACCESS_KEY,
  secretAccessKey: process.env.AWS_SELLER_SECRET_KEY,
});

interface ResolveCustomerResponse {
  CustomerIdentifier: any;
  CustomerAWSAccountId: any;
  ProductCode: any;
}

async function subscribeCustomer(customerDetails: any) {
  return fetch(servicesEndpoint + '/marketplace/subscribe', {
    headers: {
      'Content-Type': 'application/json',
      'X-ACTION-TOKEN': servicesServerToken,
    },
    body: JSON.stringify({
      customer_identifier: customerDetails.CustomerIdentifier,
      provider_account_id: customerDetails.CustomerAWSAccountId,
      product_code: customerDetails.ProductCode,
      marketplace: 'aws',
    }),
    method: 'POST',
  });
}

function sendErrorResponse(res: NextApiResponse) {
  res.setHeader('Content-Type', 'text/html').status(200).send(getErrorHtml());
}

async function resolveCustomer(token: string): Promise<ResolveCustomerResponse> {
  const params = {
    RegistrationToken: token,
  };
  try {
    const data = await marketplaceMetering.resolveCustomer(params).promise();
    return {
      CustomerIdentifier: data.CustomerIdentifier,
      CustomerAWSAccountId: data.CustomerAWSAccountId,
      ProductCode: data.ProductCode,
    };
  } catch (err) {
    console.error('Error resolving customer:', err);
    throw new Error('Failed to resolve customer');
  }
}

function getRequestId(req: NextApiRequest): string {
  const requestIds = req.headers['x-request-id'];
  return Array.isArray(requestIds) ? requestIds[0] : requestIds ?? uuidv4();
}

export default async function handler(req: NextApiRequest, res: NextApiResponse) {
  const token = req.body['x-amzn-marketplace-token'] as string | undefined;

  if (!token) {
    return res.status(400).send('Missing marketplace token');
  }

  try {
    const customerDetails = await resolveCustomer(token);
    const subscriptionResponse = await subscribeCustomer(customerDetails);

    if (subscriptionResponse.ok) {
      const responseJson = await subscriptionResponse.json();
      const html =
        responseJson.tenant_id == null ? getHtmlForGuest(`${process.env.BASE_URL}/api/marketplace/new`, customerDetails) : getAccountExistsHtml();

      res.setHeader('Content-Type', 'text/html').setHeader('x-request-id', getRequestId(req)).status(200).send(html);
    } else {
      sendErrorResponse(res);
    }
  } catch (err) {
    console.error('Error during marketplace subscription:', err);
    sendErrorResponse(res);
  }
}
