const path = require('path');

const withBundleAnalyzer = require('@next/bundle-analyzer')({
  enabled: process.env.ANALYZE === 'true',
});

/** @type {import('next').NextConfig} */
const nextConfig = {
  // Core Next.js flags
  reactStrictMode: true,
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
