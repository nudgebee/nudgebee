# syntax=docker/dockerfile:1.7
FROM node:25-alpine AS base
# Refresh the base image's Alpine OS packages to the latest patch level. The
# node:25-alpine tag lags Alpine security releases (e.g. openssl/libssl3), so
# without this the runtime inherits fixed-upstream OS CVEs. Applied on the shared
# base so deps/builder/runner all get the patched packages.
RUN apk upgrade --no-cache

# Install dependencies only when needed
FROM base AS deps
# Check https://github.com/nodejs/docker-node/tree/b4117f9333da4138b03a546ec926ef50a31506c3#nodealpine to understand why libc6-compat might be needed.
RUN apk add --no-cache libc6-compat
WORKDIR /app

# Install dependencies based on the preferred package manager
COPY package.json yarn.lock* package-lock.json* pnpm-lock.yaml* ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --legacy-peer-deps


# Rebuild the source code only when needed
FROM base AS builder
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules

# Copy configuration files first to leverage caching
COPY package.json package-lock.json* ./
COPY next.config.js ./
COPY tsconfig.json ./
COPY postcss.config.js ./
# Copy other necessary config files if they exist, e.g., babel.config.js

# Copy the rest of the application code
COPY . .

# Next.js collects completely anonymous telemetry data about general usage.
# Learn more here: https://nextjs.org/telemetry
# Uncomment the following line in case you want to disable telemetry during the build.
# ENV NEXT_TELEMETRY_DISABLED 1

ENV NODE_OPTIONS="--max_old_space_size=8192"
RUN --mount=type=cache,target=/app/.next/cache \
    npm run build --legacy-peer-deps

# Production image, copy all the files and run next
FROM base AS runner
WORKDIR /app

ENV NODE_ENV production
# Uncomment the following line in case you want to disable telemetry during runtime.
# ENV NEXT_TELEMETRY_DISABLED 1

RUN addgroup --system --gid 1001 nodejs
RUN adduser --system --uid 1001 nextjs

COPY --from=builder /app/public ./public

# Set the correct permission for prerender cache
RUN mkdir .next
RUN chown nextjs:nodejs .next

# Automatically leverage output traces to reduce image size
# https://nextjs.org/docs/advanced-features/output-file-tracing
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static

# Drop the base image's bundled npm from the runtime. A Next.js standalone
# server runs via `node server.js` and never invokes npm at runtime; npm's
# vendored node_modules (sigstore, tar, picomatch, ip-address, brace-expansion,
# @sigstore/*) are the sole source of this image's flagged CVEs, so removing
# them clears all of them with zero functional impact. Runs as root before the
# USER switch below.
RUN rm -rf /usr/local/lib/node_modules/npm /usr/local/bin/npm /usr/local/bin/npx

USER nextjs

EXPOSE 3000
ARG NEXT_PUBLIC_APP_VERSION_ARG
ENV NEXT_PUBLIC_APP_VERSION=${NEXT_PUBLIC_APP_VERSION_ARG:-0.0.0}

ENV PORT 3000
# set hostname to localhost
ENV HOSTNAME "0.0.0.0"

CMD ["node", "server.js"]
