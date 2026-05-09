/**
 * Environment configuration for E2E tests
 * Supports multiple environments (test, prod) with environment-specific settings
 */

export interface EnvironmentConfig {
  name: string;
  baseUrl: string;
  cluster: string;
  tenant: string;
}

/**
 * Available environments configuration
 * Default values can be overridden via environment variables
 */
export const environments: Record<string, EnvironmentConfig> = {
  test: {
    name: "test",
    baseUrl: "https://test.nudgebee.pollux.in",
    cluster: process.env.CLUSTER_NAME || "iteration-test",
    tenant: process.env.SWITCH_TENANT || "Iteration-150",
  },
  prod: {
    name: "prod",
    baseUrl: "https://app.nudgebee.pollux.in",
    cluster: process.env.PROD_CLUSTER_NAME || "production",
    tenant: process.env.PROD_SWITCH_TENANT || "Production",
  },
};

/**
 * Get the current environment configuration
 * Defaults to 'test' environment if E2E_ENVIRONMENT is not set
 * This preserves backward compatibility with existing tests
 */
export function getEnvironment(): EnvironmentConfig {
  const envName = process.env.E2E_ENVIRONMENT || "test";
  return environments[envName] || environments.test;
}

/**
 * Get environment name for logging/reporting
 */
export function getEnvironmentName(): string {
  return getEnvironment().name;
}
