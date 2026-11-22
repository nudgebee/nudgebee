// Mirrors carriesGatewayTraffic in api-server/services/user/token_policy.go and the kind
// filter in llm/gateway/auth/user_auths.go — one place, so the UI and server cannot drift.
export const carriesGatewayTraffic = (kind) => kind === 'gateway' || kind === 'all';

// Kinds that open the app API, so may carry scopes (normalizeScopes in token_policy.go).
export const carriesPlatformTraffic = (kind) => kind === 'platform' || kind === 'all';

export const KIND_LABELS = { platform: 'Platform', gateway: 'AI Gateway', all: 'All access' };
