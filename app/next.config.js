const path = require('path');

const withBundleAnalyzer = require('@next/bundle-analyzer')({
  enabled: process.env.ANALYZE === 'true',
});

/** @type {import('next').NextConfig} */
const nextConfig = {
  // Core Next.js flags
  reactStrictMode: true,
  async headers() {
    // Anti-framing (clickjacking) + standard hardening applied to every
    // response. CSP here only carries frame-ancestors — it does not restrict
    // scripts/styles, so it is additive to any CSP set upstream at the ingress.
    const securityHeaders = [
      { key: 'X-Frame-Options', value: 'SAMEORIGIN' },
      { key: 'Content-Security-Policy', value: "frame-ancestors 'self'" },
      { key: 'X-Content-Type-Options', value: 'nosniff' },
      { key: 'Referrer-Policy', value: 'strict-origin-when-cross-origin' },
    ];
    // HSTS only outside development: a 2-year includeSubDomains+preload pin can
    // wedge local work on HTTPS dev hosts (and browsers ignore it over plain HTTP).
    if (process.env.NODE_ENV !== 'development') {
      securityHeaders.push({
        key: 'Strict-Transport-Security',
        value: 'max-age=63072000; includeSubDomains; preload',
      });
    }
    return [
      { source: '/:path*', headers: securityHeaders },
      {
        // Authenticated API data (GraphQL gateway + RPC gateway) must never be
        // cached by browsers/proxies/CDNs.
        source: '/api/:gateway(graphql|rpc)',
        headers: [{ key: 'Cache-Control', value: 'no-store, no-cache, must-revalidate, private' }],
      },
    ];
  },
  async redirects() {
    return [
      {
        source: '/kubernetes/investigate',
        has: [{ type: 'query', key: 'id' }],
        destination: '/investigate?source=kubernetes',
        permanent: true,
      },
      {
        source: '/cloud-account/investigate',
        has: [{ type: 'query', key: 'id' }],
        destination: '/investigate?source=cloud',
        permanent: true,
      },
    ];
  },
  async rewrites() {
    return [
      {
        source: '/.well-known/microsoft-identity-association.json',
        destination: '/api/well-known/microsoft-identity-association',
      },
    ];
  },
  poweredByHeader: false,
  output: 'standalone',
  // Ensure actions.yaml/.graphql (used by /api/rpc + /api/graphql bypass) are
  // included in the standalone build — read at runtime via fs, so Next's
  // tracer can't infer the dependency.
  outputFileTracingIncludes: {
    '/api/rpc': ['./src/lib/actions.yaml', './src/lib/actions.graphql'],
    '/api/graphql': ['./src/lib/actions.yaml', './src/lib/actions.graphql'],
  },
  // Environment variables (safe for Turbopack)
  env: {
    NEXT_PUBLIC_APP_VERSION: process.env.NEXT_PUBLIC_APP_VERSION,
  },
  // Sass support (fully Turbopack-compatible)
  sassOptions: {
    includePaths: [path.join(__dirname, 'styles')],
  },
  // Server-only packages (good for OpenTelemetry)
  serverExternalPackages: ['@opentelemetry/auto-instrumentations-node', '@opentelemetry/sdk-node'],
  // Turbopack configuration
  turbopack: {
    resolveAlias: {
      '@': path.join(__dirname, 'src'),
    },
    resolveExtensions: ['.mdx', '.tsx', '.ts', '.jsx', '.js', '.mjs', '.json'],
    rules: {
      '*.icon.svg': {
        loaders: [
          {
            loader: '@svgr/webpack',
            options: {
              dimensions: false,
              svgoConfig: {
                plugins: [
                  {
                    name: 'preset-default',
                    params: {
                      overrides: {
                        removeViewBox: false,
                      },
                    },
                  },
                ],
              },
            },
          },
        ],
        as: '*.js',
      },
    },
  },
  webpack(config) {
    const fileLoaderRule = config.module.rules.find((rule) => rule.test?.test?.('.svg'));

    config.module.rules.push({
      test: /\.icon\.svg$/i,
      use: [
        {
          loader: '@svgr/webpack',
          options: {
            dimensions: false,
            svgoConfig: {
              plugins: [
                {
                  name: 'preset-default',
                  params: {
                    overrides: {
                      removeViewBox: false,
                    },
                  },
                },
              ],
            },
          },
        },
      ],
    });

    if (fileLoaderRule) {
      fileLoaderRule.exclude = /\.icon\.svg$/i;
    }

    return config;
  },
};
module.exports = withBundleAnalyzer(nextConfig);
