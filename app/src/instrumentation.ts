// instrumentation.ts

// Warn (but don't refuse to boot) when the any-email/any-password dummy creds
// provider is enabled AND BASE_URL is non-loopback. The combo is safe for
// laptop dev (BASE_URL=http://localhost:3000) but turns any network-reachable
// deploy into "first person to hit the signin URL becomes tenant_admin."
//
// Why warn-only and not fatal: some operators run internal-only deploys on
// public-looking hostnames (e.g. dev.example.internal reachable only via VPN)
// where the dummy-creds provider is intentional for bootstrap testing.
// Refusing to boot would strand them; the loud warning ensures the misconfig
// is visible if it's accidental. Mirrors the H1 (compromised encryption key)
// warning pattern in api-server's config.go.
function isLoopbackBaseURL(base: string | undefined): boolean {
  if (!base) return true; // empty BASE_URL — defer to other startup checks
  let host = '';
  try {
    host = new URL(base).hostname;
  } catch {
    // Unparseable — fail-closed (treat as not-loopback, so we warn).
    return false;
  }
  if (host === 'localhost') return true;
  if (host === '[::1]' || host === '::1') return true;
  if (host.startsWith('127.')) return true; // 127.0.0.0/8
  return false;
}

function warnIfDummyCredsUnsafe(): void {
  if (process.env.NEXTAUTH_DUMMY_CREDS_ENABLED !== 'true') return;
  const base = process.env.BASE_URL;
  if (isLoopbackBaseURL(base)) return;
  console.warn(
    'SECURITY: NEXTAUTH_DUMMY_CREDS_ENABLED=true is set with non-loopback ' +
      `BASE_URL=${base || '<unset>'}. The dummy-creds provider grants tenant_admin to any email with the shared password — ` +
      'restrict network access to BASE_URL (e.g. VPN-only ingress), or set NEXTAUTH_DUMMY_CREDS_ENABLED=false. ' +
      'This warning fires on every boot until resolved.'
  );
}

export async function register() {
  if (process.env.NEXT_RUNTIME === 'nodejs') {
    warnIfDummyCredsUnsafe();
  }

  // Register the server-side GraphQL gateway on globalThis so queryGraphQL()
  // in HttpService.ts can invoke it without statically importing rpcGateway
  // (which would pull fs/bcrypt/yaml into the browser bundle and break the
  // build). instrumentation.ts is server-only by Next.js convention, so
  // the dynamic import below is never resolved for the client.
  if (process.env.NEXT_RUNTIME === 'nodejs') {
    try {
      const { bypassGraphQLAsServer } = await import('@lib/rpcGateway');
      (globalThis as { __nbBypassGraphQLAsServer?: unknown }).__nbBypassGraphQLAsServer = bypassGraphQLAsServer;
      console.log('🔁 Server-side GraphQL gateway registered');
    } catch (e) {
      console.log('⚠️  Failed to register server-side GraphQL gateway:', e);
    }
  }

  if (process.env.OTEL_DISABLED === 'true') {
    console.log('🚫 OpenTelemetry is disabled.');
    return;
  }

  console.log('🧩 OpenTelemetry instrumentation initializing...');
  if (process.env.NEXT_RUNTIME === 'nodejs') {
    const { NodeSDK } = await import('@opentelemetry/sdk-node');
    const { OTLPTraceExporter } = await import('@opentelemetry/exporter-trace-otlp-http');
    const { BatchSpanProcessor, SimpleSpanProcessor } = await import('@opentelemetry/sdk-trace-base');
    const { getNodeAutoInstrumentations } = await import('@opentelemetry/auto-instrumentations-node');

    const exporterType = process.env.OTEL_EXPORTER ?? 'console';

    let exporter;
    let spanProcessor;

    if (exporterType === 'otlp') {
      exporter = new OTLPTraceExporter();
      spanProcessor = new BatchSpanProcessor(exporter);
      console.log('🟢 Using OTLP trace exporter');
    } else {
      // Compact one-line console exporter — replaces the SDK's
      // ConsoleSpanExporter (which uses console.dir-style multi-line pretty
      // print). For full span attributes, set OTEL_EXPORTER=otlp.
      exporter = {
        export(
          spans: readonly {
            name: string;
            startTime: [number, number];
            endTime: [number, number];
            attributes: Record<string, unknown>;
            status: { code: number };
            spanContext(): { traceId: string };
          }[],
          cb: (r: { code: number }) => void
        ) {
          for (const s of spans) {
            const dur = ((s.endTime[0] - s.startTime[0]) * 1000 + (s.endTime[1] - s.startTime[1]) / 1e6).toFixed(1);
            const route = (s.attributes['next.route'] || s.attributes['http.target'] || s.attributes['url.path'] || '') as string;
            const ok = s.status.code === 2 ? 'ERR' : 'OK';
            console.log(`[otel] ${ok} ${dur}ms ${s.name}${route ? ` (${route})` : ''} trace=${s.spanContext().traceId.slice(0, 8)}`);
          }
          cb({ code: 0 });
        },
        shutdown() {
          return Promise.resolve();
        },
        forceFlush() {
          return Promise.resolve();
        },
      };
      spanProcessor = new SimpleSpanProcessor(exporter);
      console.log('🟣 Using compact console trace exporter (set OTEL_EXPORTER=otlp for full attributes)');
    }

    // Initialize OpenTelemetry SDK
    const sdk = new NodeSDK({
      serviceName: process.env.OTEL_SERVICE_NAME ?? 'nextjs-app',
      spanProcessors: [spanProcessor],
      instrumentations: [
        getNodeAutoInstrumentations({
          '@opentelemetry/instrumentation-http': {
            enabled: true,
            // Trace only specific endpoints
            ignoreIncomingRequestHook: (request) => {
              const url = request.url || '';
              return !(url.includes('/api/graphql') || url.includes('/api/proxy/relay/request'));
            },
            ignoreOutgoingRequestHook: (options) => {
              const url = typeof options === 'string' ? options : options.path || options.hostname || '';
              return !(url.includes('/api/graphql') || url.includes('/api/proxy/relay/request'));
            },
          },
          '@opentelemetry/instrumentation-fs': {
            enabled: false,
          },
        }),
      ],
    });

    sdk.start();
    console.log(`✅ OpenTelemetry instrumentation started using "${exporterType}" exporter`);
  }
}
