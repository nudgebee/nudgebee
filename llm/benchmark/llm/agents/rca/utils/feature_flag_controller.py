"""Controller for OpenTelemetry Demo feature flags via flagd."""

import json
import logging
import subprocess
import time
from typing import Dict, Any, cast

logger = logging.getLogger(__name__)


class FeatureFlagController:
    """Manages feature flags in the nudgebee-demo namespace."""

    def __init__(
        self, namespace: str = "nudgebee-demo", configmap: str = "flagd-config"
    ):
        self.namespace = namespace
        self.configmap = configmap
        self._original_config = None

    def _get_current_config(self) -> Dict[str, Any]:
        """Retrieve current flagd configuration."""
        result = subprocess.run(
            [
                "kubectl",
                "get",
                "configmap",
                self.configmap,
                "-n",
                self.namespace,
                "-o",
                "jsonpath={.data.demo\\.flagd\\.json}",
            ],
            capture_output=True,
            text=True,
            check=True,
        )
        return cast(Dict[str, Any], json.loads(result.stdout))

    def _update_config(self, config: Dict[str, Any]) -> None:
        """Update flagd configuration."""
        config_json = json.dumps(config)
        patch = {"data": {"demo.flagd.json": config_json}}
        patch_json = json.dumps(patch)

        subprocess.run(
            [
                "kubectl",
                "patch",
                "configmap",
                self.configmap,
                "-n",
                self.namespace,
                "--type",
                "merge",
                "-p",
                patch_json,
            ],
            check=True,
            capture_output=True,
        )
        logger.info(f"Updated configmap {self.configmap}")

        # Restart flagd to pick up changes
        self._restart_flagd()

    def _restart_flagd(self) -> None:
        """Restart flagd deployment to apply new configuration."""
        subprocess.run(
            ["kubectl", "rollout", "restart", "deployment/flagd", "-n", self.namespace],
            check=True,
            capture_output=True,
        )

        # Wait for rollout to complete
        subprocess.run(
            [
                "kubectl",
                "rollout",
                "status",
                "deployment/flagd",
                "-n",
                self.namespace,
                "--timeout=60s",
            ],
            check=True,
            shell=False,
            capture_output=True,
        )
        logger.info("Flagd restarted successfully")

        # Give services time to pick up new flags
        time.sleep(10)

    def enable_flag(self, flag_name: str, variant: str = "on") -> None:
        """Enable a specific feature flag.

        Args:
            flag_name: Name of the feature flag (e.g., 'productCatalogFailure')
            variant: Variant to enable (default: 'on')
        """
        config = self._get_current_config()

        if flag_name not in config["flags"]:
            raise ValueError(f"Flag '{flag_name}' not found in configuration")

        # Backup original if first change
        if self._original_config is None:
            self._original_config = json.loads(json.dumps(config))

        config["flags"][flag_name]["defaultVariant"] = variant
        self._update_config(config)
        logger.info(f"Enabled flag '{flag_name}' with variant '{variant}'")

    def disable_flag(self, flag_name: str) -> None:
        """Disable a specific feature flag."""
        config = self._get_current_config()

        if flag_name not in config["flags"]:
            raise ValueError(f"Flag '{flag_name}' not found in configuration")

        config["flags"][flag_name]["defaultVariant"] = "off"
        self._update_config(config)
        logger.info(f"Disabled flag '{flag_name}'")

    def reset_all_flags(self) -> None:
        """Reset all flags to their original state."""
        if self._original_config:
            self._update_config(self._original_config)
            logger.info("Reset all flags to original state")
        else:
            # Set all to off
            config = self._get_current_config()
            for flag_name in config["flags"]:
                config["flags"][flag_name]["defaultVariant"] = "off"
            self._update_config(config)
            logger.info("Disabled all flags")

    def get_flag_status(self, flag_name: str) -> str:
        """Get current status of a flag."""
        config = self._get_current_config()
        if flag_name not in config["flags"]:
            raise ValueError(f"Flag '{flag_name}' not found")
        return cast(str, config["flags"][flag_name]["defaultVariant"])

    def wait_for_telemetry(self, seconds: int = 60) -> None:
        """Wait for telemetry data to accumulate after flag change."""
        logger.info(f"Waiting {seconds}s for telemetry to accumulate...")
        time.sleep(seconds)


# --- Module-level hooks for unified benchmark runner (config.yaml references) ---

_controller = FeatureFlagController(namespace="nudgebee-demo")


def rca_before_query(test_case):
    """Enable feature flag and wait for telemetry before each RCA scenario."""
    feature_flag = test_case.get("feature_flag")
    if not feature_flag:
        return
    flag_variant = test_case.get("flag_variant", "on")
    wait_time = test_case.get("wait_time_seconds", 60)
    _controller.enable_flag(feature_flag, flag_variant)
    _controller.wait_for_telemetry(wait_time)


def rca_after_query(test_case):
    """Disable feature flag after each RCA scenario."""
    feature_flag = test_case.get("feature_flag")
    if not feature_flag:
        return
    _controller.disable_flag(feature_flag)
    time.sleep(10)


def rca_signal_enricher(result, test_case, llm):
    """Analyze which telemetry signals the agent used during investigation."""
    pass
