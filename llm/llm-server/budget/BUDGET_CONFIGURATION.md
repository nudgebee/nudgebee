# LLM Budget Configuration Guide

This document provides comprehensive guidance on configuring monthly budget limits for LLM usage in the Nudgebee platform.

## Table of Contents

- [Overview](#overview)
- [Budget Hierarchy](#budget-hierarchy)
- [Configuration Attributes](#configuration-attributes)
- [SQL Insert Statements](#sql-insert-statements)
- [Common Use Cases](#common-use-cases)
- [Query Configurations](#query-configurations)
- [API Endpoints Covered](#api-endpoints-covered)

## Overview

The budget system provides **two types of monthly limits** that automatically reset on the 1st of each month:

1. **Budget Limits** (cost-based): Monthly token usage cost limits in USD
2. **Count Limits** (rate-based): Monthly conversation count limits

### Limit Levels

- **Budget Limits**: Configured at two levels
  - **Tenant Level** (organization-wide): Applies to all accounts under the tenant
  - **Account Level** (per-account): More granular control for individual cloud accounts

- **Count Limits**: Configured at tenant level only
  - **Tenant Level** (organization-wide): Rate limiting for entire organization

### Supported Modules

- `investigation` - For event analysis, RCA, and timeline endpoints
- `user_investigation` - For general chat completion endpoints

## Budget Hierarchy

The system uses a **2-layer fallback approach** for budget limits: Database → System Defaults

### Check Order

The system checks limits in the following order:

1. **Tenant Budget Disabled Check**
   - Check if tenant budget disabled flag is set (skip budget checks if disabled)
   - Budget disabled flag does NOT affect count checks (independent controls)

2. **Tenant Budget Limit Check** (organization-wide, cost-based)
   - Get tenant budget limit (DB or system default)
   - Check if tenant usage exceeds limit
   - Deny if exceeded with: "monthly budget limit exceeded for your organization"

3. **Tenant Count Limit Enabled Check**
   - Check if tenant count limit is enabled (skip count check if not enabled)
   - Count limit is INDEPENDENT of budget disabled flag

4. **Tenant Count Limit Check** (organization-wide, rate-based)
   - Get tenant count limit (DB or system default)
   - Check if conversation count exceeds limit
   - Deny if exceeded with: "monthly investigation count limit exceeded for your organization"

5. **Account Budget Disabled Check**
   - Check if account budget disabled flag is set (skip account budget if disabled)

6. **Account Budget Limit Check** (per-account, cost-based)
   - Get account budget limit (DB or system default)
   - Check if account usage exceeds limit
   - Deny if exceeded with: "monthly budget limit exceeded for this account"

**Important**: Budget and count limits are **INDEPENDENT**:
- Premium tenants can have unlimited budget (`LLM_BUDGET_DISABLED`) but still have count limits enabled
- Count limit has its own enabled flag (`llm_count_limit_enabled_*`)
- This allows flexible configurations like "unlimited cost but max 200 investigations/month"

### Disabled Flag Checks

**Tenant Budget Disabled Flag**
- If feature flag `LLM_BUDGET_DISABLED_<MODULE>` is enabled for tenant, **skip budget checks only** (steps 2 and 6)
- Source: `feature_flag` table
- **Important**: This does NOT skip count limit checks (count is independent)
- Count limits can still apply even when budget is disabled

**Tenant Count Limit Enabled Flag**
- If `llm_count_limit_enabled_<module>` is `true`, count limit checks are performed
- Source: `tenant_attrs` table
- Default: `false` (count limits disabled by default)
- Independent of budget disabled flag

**Account Budget Disabled Flag**
- If `llm_budget_disabled_<module>` is `true` at account level, **skip account budget check only**
- Source: `cloud_account_attrs` table
- Note: This only skips the account-level check; tenant-level checks still apply

### Budget Limit Resolution (2-Layer Fallback)

When determining the budget limit for tenant or account:

**Layer 1: Database Configuration**
- **Tenant**: Check `tenant_attrs` table for `llm_budget_limit_<module>`
- **Account**: Check `cloud_account_attrs` table for `llm_budget_limit_<module>`
- If found → Use this value

**Layer 2: System Default Configuration** (when no DB entry)
- Uses module-specific config defaults from application configuration
- **Tenant defaults:**
  - Investigation: `1000.0` USD/month (`config.TenantLlmDefaultBudgetLimitInvestigation`)
  - User Investigation: `1000.0` USD/month (`config.TenantLlmDefaultBudgetLimitUserInvestigation`)
  - Unknown modules: `500.0` USD/month (fallback constant)
- **Account defaults:**
  - Investigation: `600.0` USD/month (`config.AccountLlmDefaultBudgetLimitInvestigation`)
  - User Investigation: `400.0` USD/month (`config.AccountLlmDefaultBudgetLimitUserInvestigation`)
  - Unknown modules: `100.0` USD/month (fallback constant)

**Important:** Account defaults are intentionally lower than tenant defaults to prevent budget hogging

### Visual Flow

```
Request comes in
    ↓
1. TENANT BUDGET CHECK (organization-wide, checked first)
    ↓
Is tenant budget disabled for this module? (feature_flag LLM_BUDGET_DISABLED_<MODULE>)
    ↓ YES → Allow (skip all budget checks)
    ↓ NO
        ↓
Get tenant budget limit:
    ├─ Found in tenant_attrs table? → Use it
    └─ Not found? → Use system default:
        - investigation: $1000 (config.TenantLlmDefaultBudgetLimitInvestigation)
        - user_investigation: $1000 (config.TenantLlmDefaultBudgetLimitUserInvestigation)
        - unknown: $500 (TenantDefaultBudgetLimitFallback)
        ↓
Check if tenant usage > tenant budget limit
    ↓ YES → Deny (429 - monthly budget exceeded for your organization)
    ↓ NO
        ↓
2. ACCOUNT BUDGET CHECK (per-account, checked second)
    ↓
Is account budget disabled for this module? (cloud_account_attrs llm_budget_disabled_<module>)
    ↓ YES → Allow (skip account budget check only)
    ↓ NO
        ↓
Get account budget limit:
    ├─ Found in cloud_account_attrs table? → Use it
    └─ Not found? → Use system default:
        - investigation: $600 (config.AccountLlmDefaultBudgetLimitInvestigation)
        - user_investigation: $400 (config.AccountLlmDefaultBudgetLimitUserInvestigation)
        - unknown: $100 (AccountDefaultBudgetLimitFallback)
        ↓
Check if account usage > account budget limit
    ↓ YES → Deny (429 - monthly budget exceeded for this account)
    ↓ NO → Allow (proceed with request)
```

## Configuration Attributes

### Tenant-Level Budget Limits (table: `tenant_attrs`)

| Attribute Name | Type | Description | Example Value |
|----------------|------|-------------|---------------|
| `llm_budget_limit_investigation` | float | Monthly budget limit for investigation (USD) | `'1000.0'` |
| `llm_budget_limit_user_investigation` | float | Monthly budget limit for user investigation (USD) | `'500.0'` |

### Tenant-Level Count Limits (table: `tenant_attrs`)

| Attribute Name | Type | Description | Example Value |
|----------------|------|-------------|---------------|
| `llm_count_limit_investigation` | integer | Monthly conversation count limit for investigation | `'100'` |
| `llm_count_limit_user_investigation` | integer | Monthly conversation count limit for user investigation | `'500'` |
| `llm_count_limit_enabled_investigation` | boolean | Enable count limit checking for investigation | `'true'` |
| `llm_count_limit_enabled_user_investigation` | boolean | Enable count limit checking for user investigation | `'true'` |

**Notes**:
- Count limit of `0` means **block all conversations** (for unlimited, set enabled flag to `false`)
- Count limits are only enforced when enabled flag is `true`
- Count limits are **independent** of budget disabled flags
- Count limits apply at tenant level only (no account-level count limits)

### Tenant-Level Disabled Flags (table: `feature_flag`)

| Feature ID | Description | Status |
|------------|-------------|--------|
| `LLM_BUDGET_DISABLED_INVESTIGATION` | Disable budget checks for investigation module | `'enabled'` to disable checks |
| `LLM_BUDGET_DISABLED_USER_INVESTIGATION` | Disable budget checks for user investigation module | `'enabled'` to disable checks |

**Note:** Feature flags require entries in the `feature` table first. These are added via migration `1763234757000_V591_insert_llm_budget_disabled_features`. Feature flag IDs use UPPERCASE naming convention.

### Account-Level Attributes (table: `cloud_account_attrs`)

| Attribute Name | Type | Description | Example Value |
|----------------|------|-------------|---------------|
| `llm_budget_limit_investigation` | float | Monthly budget limit for investigation (USD) | `'200.0'` |
| `llm_budget_limit_user_investigation` | float | Monthly budget limit for user investigation (USD) | `'100.0'` |
| `llm_budget_disabled_investigation` | boolean | Disable budget checks for investigation | `'true'` |
| `llm_budget_disabled_user_investigation` | boolean | Disable budget checks for user investigation | `'false'` |

## SQL Insert Statements

### Tenant-Level Budget Configurations

```sql
-- Set tenant budget limit for investigation module
INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('your-tenant-id-here', 'llm_budget_limit_investigation', '1000.0', NOW(), NOW())
ON CONFLICT (tenant_id, name)
DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

-- Set tenant budget limit for user_investigation module
INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('your-tenant-id-here', 'llm_budget_limit_user_investigation', '500.0', NOW(), NOW())
ON CONFLICT (tenant_id, name)
DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

-- Disable budget checking for tenant's investigation module (uses feature_flag table)
INSERT INTO feature_flag (feature_id, tenant_id, status, created_at)
VALUES ('LLM_BUDGET_DISABLED_INVESTIGATION', 'your-tenant-uuid-here', 'enabled', NOW());

-- Disable budget checking for tenant's user_investigation module (uses feature_flag table)
INSERT INTO feature_flag (feature_id, tenant_id, status, created_at)
VALUES ('LLM_BUDGET_DISABLED_USER_INVESTIGATION', 'your-tenant-uuid-here', 'enabled', NOW());
```

### Tenant-Level Count Limit Configurations

```sql
-- Set tenant count limit for investigation module
INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('your-tenant-id-here', 'llm_count_limit_investigation', '100', NOW(), NOW())
ON CONFLICT (tenant_id, name)
DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

-- Enable count limit checking for investigation module
INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('your-tenant-id-here', 'llm_count_limit_enabled_investigation', 'true', NOW(), NOW())
ON CONFLICT (tenant_id, name)
DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

-- Set tenant count limit for user_investigation module
INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('your-tenant-id-here', 'llm_count_limit_user_investigation', '500', NOW(), NOW())
ON CONFLICT (tenant_id, name)
DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

-- Enable count limit checking for user_investigation module
INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('your-tenant-id-here', 'llm_count_limit_enabled_user_investigation', 'true', NOW(), NOW())
ON CONFLICT (tenant_id, name)
DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
```

### Account-Level Budget Configurations

```sql
-- Set account budget limit for investigation module
INSERT INTO cloud_account_attrs (cloud_account_id, name, value, created_at, updated_at)
VALUES ('your-account-id-here', 'llm_budget_limit_investigation', '200.0', NOW(), NOW())
ON CONFLICT (cloud_account_id, name)
DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

-- Set account budget limit for user_investigation module
INSERT INTO cloud_account_attrs (cloud_account_id, name, value, created_at, updated_at)
VALUES ('your-account-id-here', 'llm_budget_limit_user_investigation', '100.0', NOW(), NOW())
ON CONFLICT (cloud_account_id, name)
DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

-- Disable budget checking for account's investigation module
INSERT INTO cloud_account_attrs (cloud_account_id, name, value, created_at, updated_at)
VALUES ('your-account-id-here', 'llm_budget_disabled_investigation', 'true', NOW(), NOW())
ON CONFLICT (cloud_account_id, name)
DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

-- Disable budget checking for account's user_investigation module
INSERT INTO cloud_account_attrs (cloud_account_id, name, value, created_at, updated_at)
VALUES ('your-account-id-here', 'llm_budget_disabled_user_investigation', 'true', NOW(), NOW())
ON CONFLICT (cloud_account_id, name)
DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
```

## Common Use Cases

### Example 1: Organization-Wide Budget

Set a tenant to allow $2000/month on investigation across all accounts:

```sql
INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('abc-123-tenant', 'llm_budget_limit_investigation', '2000.0', NOW(), NOW())
ON CONFLICT (tenant_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
```

### Example 2: Restrictive Account Budget

Limit a specific account under the tenant to only $150/month:

```sql
INSERT INTO cloud_account_attrs (cloud_account_id, name, value, created_at, updated_at)
VALUES ('xyz-456-account', 'llm_budget_limit_investigation', '150.0', NOW(), NOW())
ON CONFLICT (cloud_account_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
```

### Example 3: Premium Tenant (Unlimited)

Disable all budget checks for a premium customer:

```sql
-- Disable investigation budget checks (uses feature_flag table)
INSERT INTO feature_flag (feature_id, tenant_id, status, created_at)
VALUES ('LLM_BUDGET_DISABLED_INVESTIGATION', 'premium-tenant-uuid', 'enabled', NOW());

-- Disable user_investigation budget checks (uses feature_flag table)
INSERT INTO feature_flag (feature_id, tenant_id, status, created_at)
VALUES ('LLM_BUDGET_DISABLED_USER_INVESTIGATION', 'premium-tenant-uuid', 'enabled', NOW());
```

### Example 4: Trial Account (Very Restrictive)

Set very low limits for trial accounts:

```sql
-- $10/month for investigation
INSERT INTO cloud_account_attrs (cloud_account_id, name, value, created_at, updated_at)
VALUES ('trial-account-id', 'llm_budget_limit_investigation', '10.0', NOW(), NOW())
ON CONFLICT (cloud_account_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

-- $5/month for user_investigation
INSERT INTO cloud_account_attrs (cloud_account_id, name, value, created_at, updated_at)
VALUES ('trial-account-id', 'llm_budget_limit_user_investigation', '5.0', NOW(), NOW())
ON CONFLICT (cloud_account_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
```

### Example 5: Premium Tenant with Count Limits (Unlimited Budget, Rate Limited)

Premium tenant with unlimited budget but rate-limited by conversation count to prevent abuse:

```sql
-- Disable budget checks (unlimited cost)
INSERT INTO feature_flag (feature_id, tenant_id, status, created_at)
VALUES ('LLM_BUDGET_DISABLED_INVESTIGATION', 'premium-tenant-uuid', 'enabled', NOW());

-- Enable count limits (rate limiting)
INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('premium-tenant-uuid', 'llm_count_limit_investigation', '200', NOW(), NOW())
ON CONFLICT (tenant_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('premium-tenant-uuid', 'llm_count_limit_enabled_investigation', 'true', NOW(), NOW())
ON CONFLICT (tenant_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
```

**Use case**: "Give unlimited budget but max 200 investigations/month to prevent API abuse"

### Example 6: Standard Tenant (Budget + Count Limits)

Standard tenant with both budget and count limits:

```sql
-- Budget limits
INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('standard-tenant-id', 'llm_budget_limit_investigation', '1000.0', NOW(), NOW())
ON CONFLICT (tenant_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

-- Count limits
INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('standard-tenant-id', 'llm_count_limit_investigation', '100', NOW(), NOW())
ON CONFLICT (tenant_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('standard-tenant-id', 'llm_count_limit_enabled_investigation', 'true', NOW(), NOW())
ON CONFLICT (tenant_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
```

**Behavior**: Request denied if either budget exceeded OR count exceeded

### Example 7: Trial Tenant (Very Restrictive Count)

Trial tenant with very low count limit:

```sql
-- Low budget limit
INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('trial-tenant-id', 'llm_budget_limit_investigation', '10.0', NOW(), NOW())
ON CONFLICT (tenant_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

-- Very low count limit (5 investigations only)
INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('trial-tenant-id', 'llm_count_limit_investigation', '5', NOW(), NOW())
ON CONFLICT (tenant_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('trial-tenant-id', 'llm_count_limit_enabled_investigation', 'true', NOW(), NOW())
ON CONFLICT (tenant_id, name) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();
```

**Behavior**: Hits count limit at 5 investigations, even if budget remaining
```

## Query Configurations

### View All Tenant Budget Configurations

```sql
SELECT
    tenant_id,
    name,
    value,
    created_at,
    updated_at
FROM tenant_attrs
WHERE name LIKE 'llm_budget_%'
ORDER BY tenant_id, name;
```

### View All Account Budget Configurations

```sql
SELECT
    cloud_account_id,
    name,
    value,
    created_at,
    updated_at
FROM cloud_account_attrs
WHERE name LIKE 'llm_budget_%'
ORDER BY cloud_account_id, name;
```

### View Configuration for Specific Tenant

```sql
SELECT
    tenant_id,
    name,
    value,
    created_at,
    updated_at
FROM tenant_attrs
WHERE tenant_id = 'your-tenant-id-here'
  AND name LIKE 'llm_budget_%'
ORDER BY name;
```

### View Configuration for Specific Account

```sql
SELECT
    cloud_account_id,
    name,
    value,
    created_at,
    updated_at
FROM cloud_account_attrs
WHERE cloud_account_id = 'your-account-id-here'
  AND name LIKE 'llm_budget_%'
ORDER BY name;
```

## Delete Configurations

### Remove Tenant Budget Limit

```sql
DELETE FROM tenant_attrs
WHERE tenant_id = 'your-tenant-id-here'
  AND name = 'llm_budget_limit_investigation';
```

### Remove Account Budget Limit

```sql
DELETE FROM cloud_account_attrs
WHERE cloud_account_id = 'your-account-id-here'
  AND name = 'llm_budget_limit_user_investigation';
```

### Re-enable Budget Checking (Tenant)

Remove the feature flag to re-enable budget checks for tenant:

```sql
-- Option 1: Delete the feature flag entry
DELETE FROM feature_flag
WHERE feature_id = 'LLM_BUDGET_DISABLED_INVESTIGATION'
  AND tenant_id = 'your-tenant-uuid-here';

-- Option 2: Set status to 'disabled'
UPDATE feature_flag
SET status = 'disabled'
WHERE feature_id = 'LLM_BUDGET_DISABLED_INVESTIGATION'
  AND tenant_id = 'your-tenant-uuid-here';
```

### Re-enable Budget Checking (Account)

Remove the disabled flag from cloud_account_attrs:

```sql
DELETE FROM cloud_account_attrs
WHERE cloud_account_id = 'your-account-id-here'
  AND name = 'llm_budget_disabled_investigation';
```

### Remove Count Limit

```sql
-- Remove count limit
DELETE FROM tenant_attrs
WHERE tenant_id = 'your-tenant-id-here'
  AND name = 'llm_count_limit_investigation';

-- Remove count enabled flag
DELETE FROM tenant_attrs
WHERE tenant_id = 'your-tenant-id-here'
  AND name = 'llm_count_limit_enabled_investigation';
```

### Disable Count Limit Checking

```sql
-- Disable count limit without removing the limit value
UPDATE tenant_attrs
SET value = 'false', updated_at = NOW()
WHERE tenant_id = 'your-tenant-id-here'
  AND name = 'llm_count_limit_enabled_investigation';
```

## API Endpoints Covered

All the following API endpoints enforce monthly budget limits:

### User Investigation Module (`module: "user_investigation"`)

- **`POST /v1/completions/chat`**
  - Main chat completion endpoint
  - Uses `"user_investigation"` module by default
  - Automatically switches to `"investigation"` if sessionId starts with `events.SessionIdPrefixEvent` (e.g., "event-")

### Investigation Module (`module: "investigation"`)

#### HTTP API Endpoints
- **`POST /v1/completions/event`** - Event analysis completion
- **`POST /v1/completions/event/log`** - Event log analysis
- **`POST /v1/completions/event/rca`** - Root Cause Analysis (RCA)
- **`POST /v1/completions/event/timeline`** - Event timeline analysis

#### Message Queue (MQ) Triggered Analysis
- **RabbitMQ Consumer** - Async event analysis triggered from message queue
  - Queue: `config.RabbitMqTroubleshootQueue`
  - Exchange: `config.RabbitMqTroubleshootExchange`
  - When budget is exceeded, the event analysis is skipped and status is set to `FAILED` with budget error message
  - This ensures that even automated/async event analysis respects budget limits

#### Background Sync Jobs
- **Dead Worker Message Recovery** - Restarts conversations from dead workers
  - Function: `syncDeadWorkerMessages()`
  - Checks budget before restarting conversation
  - If budget exceeded: Marks conversation and message as `FAILED`, prevents infinite retry loop

- **Server Restart Recovery** - Restarts in-progress conversations on server restart
  - Function: `syncConversationMessagesOnServerRestart()`
  - Checks budget before restarting conversation
  - If budget exceeded: Marks conversation and message as `FAILED`, prevents stale records

## Budget Status Endpoint

### `POST /v1/budget/status`

Retrieves comprehensive budget status information for both tenant and account levels.

**Request:**
```json
{
  "input": {
    "request": {
      "account_id": "your-account-id"
    }
  },
  "session_variables": {
    "x-hasura-tenant-id": "tenant-uuid",
    "x-hasura-user-id": "user-uuid"
  }
}
```

**Response:**
```json
{
  "tenant_id": "890cad87-c452-4aa7-b84a-742cee0454a1",
  "account_id": "your-account-id",
  "period": "2025-11",
  "investigation": {
    "tenant": {
      "current_usage": 450.75,
      "budget_limit": 2000.00,
      "budget_remaining": 1549.25,
      "rate_limit": true,
      "limit_source": "tenant_attrs",
      "current_count": 85,
      "count_limit": 100,
      "count_remaining": 15,
      "count_enabled": true,
      "count_limit_source": "tenant_attrs"
    },
    "account": {
      "current_usage": 125.50,
      "budget_limit": 500.00,
      "budget_remaining": 374.50,
      "rate_limit": true,
      "limit_source": "config_default",
      "current_count": 0,
      "count_limit": 0,
      "count_remaining": 0,
      "count_enabled": false,
      "count_limit_source": "n/a"
    }
  },
  "user_investigation": {
    "tenant": {
      "current_usage": 320.00,
      "budget_limit": 500.00,
      "budget_remaining": 180.00,
      "rate_limit": true,
      "limit_source": "config_default",
      "current_count": 420,
      "count_limit": 500,
      "count_remaining": 80,
      "count_enabled": true,
      "count_limit_source": "tenant_attrs"
    },
    "account": {
      "current_usage": 85.25,
      "budget_limit": 200.00,
      "budget_remaining": 114.75,
      "rate_limit": true,
      "limit_source": "config_default",
      "current_count": 0,
      "count_limit": 0,
      "count_remaining": 0,
      "count_enabled": false,
      "count_limit_source": "n/a"
    }
  }
}
```

**Response Fields:**

**Budget-related fields:**
- `current_usage` - Total cost spent this month (USD)
- `budget_limit` - The effective budget limit (USD)
- `budget_remaining` - Remaining budget (`budget_limit - current_usage`, minimum 0)
- `rate_limit` - Whether budget checks are enabled (opposite of disabled flag)
- `limit_source` - Where the budget limit was configured:
  - `tenant_attrs` - From tenant_attrs table (database)
  - `cloud_account_attrs` - From cloud_account_attrs table (database)
  - `config_default` - From application system defaults (config)

**Count-related fields (tenant level only):**
- `current_count` - Total conversations created this month
- `count_limit` - Maximum conversations allowed per month (0 = block all; for unlimited set enabled=false)
- `count_remaining` - Remaining conversations (`count_limit - current_count`, minimum 0)
- `count_enabled` - Whether count limit checking is enabled
- `count_limit_source` - Where the count limit was configured:
  - `tenant_attrs` - From tenant_attrs table (database)
  - `system_default` - From application defaults (0 = block all)
  - `n/a` - Not applicable (account level, count limits only at tenant level)

## Limit Exceeded Responses

When any limit (budget or count) is exceeded, the API returns:

**HTTP Status Code:** `429 Too Many Requests`

### Budget Limit Exceeded (Tenant Level)

```json
{
  "errors": [
    {
      "message": "budget: monthly budget limit exceeded for your organization"
    }
  ]
}
```

### Budget Limit Exceeded (Account Level)

```json
{
  "errors": [
    {
      "message": "budget: monthly budget limit exceeded for this account"
    }
  ]
}
```

### Count Limit Exceeded (Tenant Level)

```json
{
  "errors": [
    {
      "message": "budget: monthly investigation count limit exceeded for your organization"
    }
  ]
}
```

**Note**: The error message uses "investigation count" regardless of module (`investigation` or `user_investigation`)

## Important Notes

1. **Budget Values**: All budget limits are in **USD dollars**
2. **Monthly Reset**: Budgets automatically reset on the **1st of each month**
3. **Data Type**: The `value` column stores strings, so numeric values must be quoted (e.g., `'1000.0'`)
4. **Boolean Values**: Disabled flag values must be `'true'` or `'false'` (as strings)
5. **Fail-Open**: If budget check fails due to errors, the system allows the request (logs error but doesn't block)
6. **Fail-Closed**: If budget is exceeded, the system denies the request with HTTP 429

## System Default Configuration

The system defaults (used when no database configuration exists) are configured in the application via environment variables or viper config.

### Tenant-Level Budget Defaults

**Investigation Module:**
- **Config Field:** `config.TenantLlmDefaultBudgetLimitInvestigation`
- **Viper Key:** `llm_default_budget_limit_tenant_investigation`
- **Environment Variable:** `LLM_DEFAULT_BUDGET_LIMIT_TENANT_INVESTIGATION`
- **Default Value:** `1000.0` USD/month

**User Investigation Module:**
- **Config Field:** `config.TenantLlmDefaultBudgetLimitUserInvestigation`
- **Viper Key:** `llm_default_budget_limit_tenant_user_investigation`
- **Environment Variable:** `LLM_DEFAULT_BUDGET_LIMIT_TENANT_USER_INVESTIGATION`
- **Default Value:** `1000.0` USD/month

**Unknown Modules:**
- **Constant:** `TenantDefaultBudgetLimitFallback`
- **Default Value:** `500.0` USD/month

### Account-Level Budget Defaults

**Investigation Module:**
- **Config Field:** `config.AccountLlmDefaultBudgetLimitInvestigation`
- **Viper Key:** `llm_default_budget_limit_account_investigation`
- **Environment Variable:** `LLM_DEFAULT_BUDGET_LIMIT_ACCOUNT_INVESTIGATION`
- **Default Value:** `600.0` USD/month

**User Investigation Module:**
- **Config Field:** `config.AccountLlmDefaultBudgetLimitUserInvestigation`
- **Viper Key:** `llm_default_budget_limit_account_user_investigation`
- **Environment Variable:** `LLM_DEFAULT_BUDGET_LIMIT_ACCOUNT_USER_INVESTIGATION`
- **Default Value:** `400.0` USD/month

**Unknown Modules:**
- **Constant:** `AccountDefaultBudgetLimitFallback`
- **Default Value:** `100.0` USD/month

### Tenant-Level Count Defaults (NEW)

**Investigation Module - Count Limit:**
- **Config Field:** `config.TenantLlmDefaultCountLimitInvestigation`
- **Viper Key:** `llm_default_count_limit_tenant_investigation`
- **Environment Variable:** `LLM_DEFAULT_COUNT_LIMIT_TENANT_INVESTIGATION`
- **Default Value:** `0` (block all; for unlimited set enabled=false)

**Investigation Module - Count Enabled:**
- **Config Field:** `config.TenantLlmDefaultCountLimitEnabledInvestigation`
- **Viper Key:** `llm_default_count_limit_enabled_tenant_investigation`
- **Environment Variable:** `LLM_DEFAULT_COUNT_LIMIT_ENABLED_TENANT_INVESTIGATION`
- **Default Value:** `false` (disabled, opt-in)

**User Investigation Module - Count Limit:**
- **Config Field:** `config.TenantLlmDefaultCountLimitUserInvestigation`
- **Viper Key:** `llm_default_count_limit_tenant_user_investigation`
- **Environment Variable:** `LLM_DEFAULT_COUNT_LIMIT_TENANT_USER_INVESTIGATION`
- **Default Value:** `0` (block all; for unlimited set enabled=false)

**User Investigation Module - Count Enabled:**
- **Config Field:** `config.TenantLlmDefaultCountLimitEnabledUserInvestigation`
- **Viper Key:** `llm_default_count_limit_enabled_tenant_user_investigation`
- **Environment Variable:** `LLM_DEFAULT_COUNT_LIMIT_ENABLED_TENANT_USER_INVESTIGATION`
- **Default Value:** `false` (disabled, opt-in)

**Note:** Count limits are only at tenant level (no account-level count limits)

### Configuration Hierarchy

The system uses a **2-layer fallback** for all configuration:

1. **Database configuration** (tenant_attrs) - highest priority
2. **System default** (environment variable or viper config) - fallback

**Example:**
```bash
# Set global default: enable count limits for all tenants
LLM_DEFAULT_COUNT_LIMIT_ENABLED_TENANT_INVESTIGATION=true
LLM_DEFAULT_COUNT_LIMIT_TENANT_INVESTIGATION=100

# Individual tenants can override in database
INSERT INTO tenant_attrs (tenant_id, name, value, created_at, updated_at)
VALUES ('special-tenant-id', 'llm_count_limit_investigation', '500', NOW(), NOW());
```

These defaults can be overridden via:
1. Environment variables (mapped through Viper)
2. Configuration files (`.env`, config files)
3. Viper SetDefault (in `config/config.go`)

## Code Architecture

### Package Structure

The budget system is organized as a standalone Go package:

```
/llm/llm-server/budget/
├── types.go                    # Constants and type definitions
├── service.go                  # Core budget checking logic
├── handler.go                  # HTTP handler helpers
├── usage.go                    # Token usage cost calculations
├── service_test.go             # Unit tests
└── BUDGET_CONFIGURATION.md     # This documentation
```

### Module Constants

Module names are defined as constants in `budget/types.go`:

```go
package budget

const (
    ModuleInvestigation     = "investigation"
    ModuleUserInvestigation = "user_investigation"
)
```

Always use these constants instead of magic strings when calling budget functions.

### Core Functions

**Budget Checking** (`budget/service.go`):
```go
// CheckBudgetLimits checks both tenant and account budget limits
// Returns true if exceeded, along with error message
budget.CheckBudgetLimits(tenantId, accountId, module string, logger *slog.Logger) (bool, string)

// GetBudgetStatus retrieves comprehensive budget status
budget.GetBudgetStatus(tenantId, accountId string, logger *slog.Logger) (*BudgetStatusResponse, error)
```

**Helper Function** (`budget/handler.go`):
```go
// CheckBudgetAndRespond checks budget and sends 429 response if exceeded
// Returns true if budget exceeded (response already sent), false to proceed
budget.CheckBudgetAndRespond(c GinContext, tenantId, accountId, module string, logger *slog.Logger) bool
```

**Usage in API handlers:**
```go
import "nudgebee/llm/budget"

if budget.CheckBudgetAndRespond(c, tenantId, accountId, budget.ModuleInvestigation, logger) {
    return // Response already sent
}
// Continue with normal processing
```

### SQL Injection Prevention

Module names are validated against a whitelist map to prevent SQL injection:

```go
var moduleQueryFilters = map[string]string{
    "investigation":      " AND c.session_id LIKE '" + events.SessionIdPrefixEvent + "%'",
    "user_investigation": "", // No additional filter
}
```

Only pre-defined static query fragments are used, never dynamic SQL construction from user input.

## Related Files

### Budget Service (Core Logic)
- **Budget Service**: `/llm/llm-server/budget/`
  - `types.go` - Module constants and type definitions
    - `ModuleInvestigation`, `ModuleUserInvestigation`
    - `BudgetLevelInfo`, `ModuleBudgetInfo`, `BudgetStatusResponse`
  - `service.go` - Core budget checking logic
    - `CheckBudgetLimits()` - Main budget checking function
    - `GetBudgetStatus()` - Get comprehensive budget status
    - `isTenantBudgetLimitExceededWithDB()` - Tenant-level check
    - `isAccountBudgetLimitExceededWithDB()` - Account-level check
  - `handler.go` - HTTP handler helpers
    - `CheckBudgetAndRespond()` - Helper for gin handlers
  - `usage.go` - Token usage calculations
    - `GetTenantTokenUsage()` - Calculate tenant's monthly usage
    - `GetAccountTokenUsage()` - Calculate account's monthly usage
    - `moduleQueryFilters` - Whitelist map for SQL injection prevention
  - `service_test.go` - Unit tests for budget service

### API Layer (HTTP Handlers)
- **Budget Status Handler**: `/llm/llm-server/api/budget.go`
  - `POST /v1/budget/status` - HTTP endpoint for retrieving budget status
  - Handles RPC action request parsing, authentication, and response formatting
  - Delegates to `budget.GetBudgetStatus()` for actual logic

### Supporting Files
- **Feature Flag Logic**: `/llm/llm-server/common/feature_flag.go`
- **Feature Flag Migration**: `/api-server/migrations/migrations/app/1763234757000_V591_insert_llm_budget_disabled_features/`
- **API Integration**:
  - `/llm/llm-server/api/chains.go` - Chat API endpoints
  - `/llm/llm-server/api/event_analyzer.go` - Event analysis HTTP API endpoints
  - `/llm/llm-server/api/event_analyzer_mq.go` - Event analysis MQ consumer
  - `/llm/llm-server/api/conversation_sync.go` - Background sync jobs
- **Configuration**: `/llm/llm-server/config/config.go`
