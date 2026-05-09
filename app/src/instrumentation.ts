// instrumentation.ts
export async function register() {
  if (process.env.OTEL_DISABLED === 'true') {
    console.log('🚫 OpenTelemetry is disabled.');
    return;
  }

  console.log('🧩 OpenTelemetry instrumentation initializing...');
  if (process.env.NEXT_RUNTIME === 'nodejs') {
    const { NodeSDK } = await import('@opentelemetry/sdk-node');
    const { OTLPTraceExporter } = await import('@opentelemetry/exporter-trace-otlp-http');
    const { ConsoleSpanExporter } = await import('@opentelemetry/sdk-trace-base');
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
      exporter = new ConsoleSpanExporter();
      spanProcessor = new SimpleSpanProcessor(exporter);
      console.log('🟣 Using Console trace exporter');
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
