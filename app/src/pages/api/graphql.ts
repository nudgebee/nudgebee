import type { NextApiRequest, NextApiResponse } from 'next';
import { handleGatewayRequest } from '@lib/graphqlGatewayHandler';

// We may stream large bodies through this handler, so the framework's 4MB
// warning is not useful signal — disable it to cut log noise. Our own
// bytes_out timing field replaces it.
//
// Request bodyParser sizeLimit is raised to 12mb so AI investigations with
// base64-inlined image attachments fit without a bespoke per-route override.
// Derivation: ~max_size_mb (1.5 default) × 1.4 base64 inflation ×
// max_per_message (4 default) + JSON/envelope headroom ≈ 12mb. Keep `/api/rpc`
// in sync — that endpoint serves the same actions for non-browser callers
// (nbctl, external API) and must accept the same payloads.
export const config = {
  api: {
    responseLimit: false,
    bodyParser: {
      sizeLimit: '12mb',
    },
  },
};

export default function handler(req: NextApiRequest, res: NextApiResponse) {
  return handleGatewayRequest(req, res, { tracerName: 'graphql-api', logPrefix: 'graphql-gateway' });
}
