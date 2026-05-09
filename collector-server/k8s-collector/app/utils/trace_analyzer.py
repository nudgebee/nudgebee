from typing import List, Dict, Any, Optional

DB_STATEMENT = "db.statement"

HTTP_STATUS_CODE = "http.status_code"


class TraceAnalyzer:
    ERROR_TYPES = {"DATABASE_EXCEPTION": "Database Exception", "DATABASE": "Database Error", "HTTP": "HTTP Error"}

    @staticmethod
    def extract_exception_details(trace: Dict[str, Any]) -> Optional[Dict[str, str]]:
        """
        Extract exception details from a trace.

        Args:
            trace (Dict[str, Any]): Trace data

        Returns:
            Optional[Dict[str, str]]: Exception details if found
        """
        attributes = trace.get("Events.Attributes", [])
        for attr in attributes:
            if not isinstance(attr, dict):
                continue

            exception_type = attr.get("exception.type", "")
            if not exception_type:
                continue
            span_attrs = trace.get("SpanAttributes", {})
            if trace.get("SpanAttributes", {}).get(DB_STATEMENT, ""):
                exception_type = "DATABASE_EXCEPTION"
            return {
                "exception_type": exception_type,
                "exception_message": attr.get("exception.message", "No details"),
                "db_statement": span_attrs.get(DB_STATEMENT, ""),
                "db_system": span_attrs.get("db.system", ""),
            }

    @staticmethod
    def is_error_trace(trace: Dict[str, Any]) -> bool:
        """
        Determine if a trace represents an error.

        Args:
            trace (Dict[str, Any]): Trace data

        Returns:
            bool: True if trace indicates an error, False otherwise
        """
        span_attrs = trace.get("SpanAttributes", trace.get("span_attributes", {}))
        return (
            trace.get("StatusCode", trace.get("status_code")) == "STATUS_CODE_ERROR"
            or span_attrs.get(HTTP_STATUS_CODE) == "500"
        )

    @classmethod
    def categorize_error(cls, trace: Dict[str, Any]) -> Optional[Dict[str, Any]]:
        """
        Categorize the type of error in a trace.

        Args:
            trace (Dict[str, Any]): Trace data

        Returns:
            Optional[Dict[str, Any]]: Categorized error details
        """
        # Database Exception Check
        exception = cls.extract_exception_details(trace)
        if exception:
            return {
                "error_type": exception.get("exception_type", "Unknown"),
                "details": exception,
                "service": trace.get("ServiceName", "Unknown"),
            }

        # Error Trace Check
        if not cls.is_error_trace(trace):
            return None

        span_attrs = trace.get("SpanAttributes", trace.get("span_attributes", {}))

        # Database Error
        if DB_STATEMENT in span_attrs:
            return {
                "error_type": "DATABASE",
                "details": {
                    "db_statement": span_attrs.get(DB_STATEMENT, ""),
                    "db_system": span_attrs.get("db.system", ""),
                    "status_code": trace.get("StatusCode", ""),
                },
                "service": trace.get("ServiceName", "Unknown"),
            }

        # HTTP Error
        if HTTP_STATUS_CODE in span_attrs:
            return {
                "error_type": "HTTP",
                "details": {
                    "http_method": span_attrs.get("http.method", ""),
                    "http_target": span_attrs.get("http.target", ""),
                    "http_status_code": span_attrs.get(HTTP_STATUS_CODE, ""),
                },
                "service": trace.get("ServiceName", "Unknown"),
            }

        return None

    @staticmethod
    def generate_error_message(error: Dict[str, Any]) -> str:
        """
        Generate a human-readable error message.

        Args:
            error (Dict[str, Any]): Error details

        Returns:
            str: Formatted error message
        """
        error_type = error["error_type"]
        service = error.get("service", "Unknown")
        details = error.get("details", {})

        if error_type == "DATABASE_EXCEPTION":
            return (
                f"Database Exception in {service}: "
                f"{details.get('exception_type', 'Unknown')} - "
                f"{details.get('exception_message', 'No details')}"
            )

        if error_type == "DATABASE":
            return f"Database Error in {service}: Failed operation on {details.get('db_system', 'Unknown')}"

        if error_type == "HTTP":
            return (
                f"HTTP Error in {service}: "
                f"{details.get('http_method', 'N/A')} "
                f"{details.get('http_target', 'N/A')} "
                f"returned {details.get('http_status_code', 'Unknown')}"
            )
        if error_type:
            return f"{error_type} in {service}: {details.get('exception_message', 'No details')}"

        return f"Unknown error in {service}: {details.get('exception_message', 'No details')}"

    @staticmethod
    def extract_service_flow(traces: List[Dict[str, Any]]) -> List[Dict[str, str]]:
        """
        Extract a unique flow of services from traces.

        Args:
            traces (List[Dict[str, Any]]): List of traces

        Returns:
            List[Dict[str, str]]: Unique service flow
        """
        seen_services = set()
        service_flow = []

        for trace in sorted(traces, key=lambda x: x.get("Timestamp", "")):
            service_name = trace.get("ServiceName", "Unknown")

            if service_name in seen_services:
                continue

            service_info = {
                "name": service_name,
                "span_name": trace.get("SpanName", trace.get("span_name", "")),
                "http_method": trace.get("SpanAttributes", {}).get("http.method", "N/A"),
            }

            service_flow.append(service_info)
            seen_services.add(service_name)

        return service_flow
