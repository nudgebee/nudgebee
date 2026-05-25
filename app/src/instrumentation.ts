// instrumentation.ts

export async function register() {
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
              return !(url.includes('/api/graphql') || url.includes('/api/relay/request'));
            },
            ignoreOutgoingRequestHook: (options) => {
              const url = typeof options === 'string' ? options : options.path || options.hostname || '';
              return !(url.includes('/api/graphql') || url.includes('/api/relay/request'));
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
