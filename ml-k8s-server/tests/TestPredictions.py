import os
import unittest

from server.metrics.prometheus_metrics import Prometheus
from server.model.hpa_model import HPAModel


class TestPredictions(unittest.TestCase):
    _namespace = "example-namespace"
    _deployment = "example-deployment"
    _container = "example-container"

    def test_get_predictions(self):
        os.environ["EPOCHS"] = "1"
        prometheus_instance = Prometheus(
            namespace_name=self._namespace, deployment_name=self._deployment, container_name=self._container
        )
        metrics_df = prometheus_instance.get_metrics()
        hpa_model = HPAModel(model_name="HPA-001", data=metrics_df)
        prediction_df = hpa_model.get_predictions(data=metrics_df)
        self.assertIsNotNone(prediction_df)

    def test_get_metrics(self):
        prometheus_instance = Prometheus(
            namespace_name=self._namespace,
            deployment_name=self._deployment,
            container_name=self._container,
            account_id="00000000-0000-0000-0000-000000000000",
        )
        result = prometheus_instance.get_metrics()
        self.assertIsNotNone(result)

    def test_metrics_http(self):
        import requests
        from opentelemetry import trace
        from opentelemetry.sdk.trace import TracerProvider
        from opentelemetry.sdk.trace.export import (
            BatchSpanProcessor,
            ConsoleSpanExporter,
        )
        from opentelemetry.trace.propagation.tracecontext import TraceContextTextMapPropagator

        provider = TracerProvider()
        processor = BatchSpanProcessor(ConsoleSpanExporter())
        provider.add_span_processor(processor)
        trace.set_tracer_provider(provider)
        tracer = trace.get_tracer(__name__)
        with tracer.start_as_current_span("test-remote-span"):
            url = "http://localhost:9999/health"
            payload = {}
            carrier = {}
            TraceContextTextMapPropagator().inject(carrier)
            header = {"traceparent": carrier["traceparent"]}
            result = requests.request("GET", url, headers=header, data=payload)
            self.assertIsNotNone(result)
