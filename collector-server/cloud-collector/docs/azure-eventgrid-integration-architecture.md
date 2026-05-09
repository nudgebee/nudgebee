# Azure Event Grid Real-time Resource Sync - Architecture Documentation

## Overview

This document describes the end-to-end architecture for real-time Azure resource state synchronization using Event Grid and Service Bus, with a focus on secure multi-tenant account/tenant mapping using tokens.

**Azure Equivalent Services:**
- AWS EventBridge → **Azure Event Grid**
- AWS SQS → **Azure Service Bus Queue**
- AWS IAM Role → **Azure Service Principal + Managed Identity**

---

## Problem Statement

### Multi-Tenant Challenge

Nudgebee's `cloud_resourses` table requires both `account` (UUID) and `tenant` (UUID) to identify resources:

```sql
SELECT * FROM cloud_resourses
WHERE account = 'f3062ba9-...'  -- UUID, not Azure subscription ID
  AND tenant = 'e88e208e-...'   -- Which tenant owns this?
  AND resourse_id = '/subscriptions/.../resourceGroups/...'
```

However, Event Grid events only provide Azure subscription IDs:

```json
{
  "subject": "/subscriptions/12345678-abcd-ef01-2345-678901234567/resourceGroups/myRG/providers/Microsoft.Compute/virtualMachines/myVM",
  "data": {
    "resourceUri": "/subscriptions/.../virtualMachines/myVM",
    "operationName": "Microsoft.Compute/virtualMachines/write",
    "status": "Succeeded"
  }
}
```

### The Multi-Tenant Problem

Query results show the same Azure subscription ID belongs to multiple tenants:

```
external_id | subscription_id                      | tenant (UUID)
------------|--------------------------------------|------------------
99          | 12345678-abcd-ef01-2345-678901234567 | f3062ba9-...     (Tenant A)
217         | 12345678-abcd-ef01-2345-678901234567 | f998385c-...     (Tenant B)
16          | 12345678-abcd-ef01-2345-678901234567 | e88e208e-...     (Tenant C)
```

**❌ Cannot lookup by `subscription_id` alone - would return wrong tenant!**

---

## Solution: Token-Based Account Mapping

### Architecture Overview

**IMPORTANT:** Customer Event Grid subscriptions send events to **Service Bus Queue directly**.
This is Azure's recommended pattern for event ingestion with high throughput and reliable delivery.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                         Customer Azure Subscription                          │
│                                                                             │
│  1. Customer gets token from Nudgebee Dashboard                            │
│     Token = NudgebeeExternalId from ARM Template                           │
│                                                                             │
│  2. ARM Template Deployed                                                  │
│     ┌──────────────────────────────────────────┐                              │
│     │ Parameters:                          │                              │
│     │ - NudgebeeExternalId: "nbee_uuid..." │                              │
│     │ - NudgebeeTenantId: tenant-uuid       │                              │
│     │ - NudgebeeSubscriptionId: sub-id     │                              │
│     └──────────────────────────────────────────┘                              │
│                                                                             │
│  3. Event Grid System Topics Created                                       │
│     ┌────────────────────────────────────┐                                    │
│     │ Subscription-level System Topic    │                                    │
│     │ - Source: Azure Resource Manager   │                                    │
│     │ - Resource Provider Events:        │                                    │
│     │   • Microsoft.Compute              │     ┌──────────────────────────┐   │
│     │   • Microsoft.Sql                  │     │ Event Grid Event:        │   │
│     │   • Microsoft.Storage              │────▶│ {                        │   │
│     │   • Microsoft.Web                  │     │   "subject": "/sub/.."   │   │
│     │   • Microsoft.ContainerService     │     │   "eventType": "write"   │   │
│     │                                    │     │   "data": {...}          │   │
│     │ Event Subscription:                │     │ }                        │   │
│     │   - Filter by operation names      │     │                          │   │
│     │   - Destination: Service Bus Queue │     │ + Advanced Filter adds:  │   │
│     │   - Advanced Filters applied       │     │   nudgebeeAccountToken   │   │
│     └────────────────────────────────────┘     └──────────────────────────┘   │
│     (+ Similar for Database, Storage, Web, AKS)                             │
└─────────────────────────────────────────────────────────────────────────────┘
                                                         │
                                                         │ Event Grid Delivery
                                                         │ to Service Bus
                                                         ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                      Nudgebee Azure Subscription                            │
│                                                                             │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ Service Bus Namespace: nudgebee-events                              │   │
│  │                                                                     │   │
│  │ Queue: resource-events                                             │   │
│  │                                                                     │   │
│  │ Access Policy:                                                     │   │
│  │ - Allow Event Grid to send messages (Send permission)             │   │
│  │ - Service Principal authentication                                 │   │
│  │                                                                     │   │
│  │ Features:                                                          │   │
│  │ - Dead Letter Queue: resource-events-dlq                          │   │
│  │ - Message Lock Duration: 5 minutes                                 │   │
│  │ - Max Delivery Count: 3                                            │   │
│  │ - Duplicate Detection: 10 minutes                                  │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                             │                                               │
│                             │ Receive messages (20s polling, 10 msgs/batch)  │
│                             ▼                                               │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ cloud-collector: StartAzureServiceBusConsumer()                     │   │
│  │                                                                     │   │
│  │ 1. Poll Service Bus queue                                          │   │
│  │ 2. Parse Event Grid event from message body                        │   │
│  │ 3. Extract: data.nudgebeeAccountToken = "nbee_abc123xyz..."       │   │
│  │ 4. Lookup account:                                                 │   │
│  │    SELECT * FROM cloud_accounts                                    │   │
│  │    WHERE external_id = 'nbee_abc123xyz...'                        │   │
│  │      AND account_number = '<tenant-id>'                           │   │
│  │    → Returns: account.id, account.tenant                          │   │
│  │                                                                     │   │
│  │ 5. Process event with TemplatedEventGridProcessor                 │   │
│  │ 6. Update cloud_resourses table:                                   │   │
│  │    UPDATE cloud_resourses                                          │   │
│  │    SET status = 'Running', last_seen = NOW()                       │   │
│  │    WHERE account = <account.id>  -- UUID from lookup              │   │
│  │      AND tenant = <account.tenant>  -- Correct tenant!            │   │
│  │      AND resourse_id = '/subscriptions/.../myVM'                   │   │
│  │                                                                     │   │
│  │ 7. Complete message (or dead-letter on failure)                   │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
│                             │                                               │
│                             ▼                                               │
│  ┌─────────────────────────────────────────────────────────────────────┐   │
│  │ PostgreSQL: cloud_resourses table                                   │   │
│  │                                                                     │   │
│  │ Resource updated with correct tenant isolation!                    │   │
│  └─────────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Key Architectural Decision: Service Bus vs Event Hubs vs Storage Queue

### Why Service Bus Queue?

**Comparison Matrix:**

| Feature | Service Bus Queue | Event Hubs | Storage Queue |
|---------|------------------|------------|---------------|
| **Event Grid Integration** | ✅ Native support | ✅ Native support | ✅ Native support |
| **Message Ordering** | ✅ FIFO with sessions | ❌ Partition-based | ❌ No guarantee |
| **Dead Letter Queue** | ✅ Built-in | ❌ Manual implementation | ❌ Manual poison queue |
| **Duplicate Detection** | ✅ Built-in | ❌ Manual | ❌ Manual |
| **Message Lock** | ✅ Automatic | ❌ N/A (streaming) | ✅ Visibility timeout |
| **Throughput** | Medium (2000 msg/sec) | Very High (millions/sec) | Low (2000 msg/sec) |
| **Cost** | Medium | High | Low |
| **Complexity** | Low | High (streaming model) | Low |
| **Message Size** | 256KB (Standard), 100MB (Premium) | 1MB | 64KB |
| **Retention** | 14 days | 7 days (up to 90) | 7 days |

**Decision: Service Bus Queue** ✅

**Reasons:**
1. **Built-in DLQ**: Automatic retry and dead-lettering for failed messages
2. **Duplicate Detection**: Prevents processing same event twice (important for idempotency)
3. **Message Lock**: Automatic lock renewal during processing
4. **Simplicity**: Queue-based model matches existing AWS SQS pattern
5. **Cost-Effective**: No need for Event Hubs' high throughput (events are <1000/sec)
6. **Event Grid Native**: Event Grid has first-class support for Service Bus

**Event Hubs** would be overkill for this use case (designed for millions of events/sec streaming).

**Storage Queue** lacks DLQ and duplicate detection, requiring custom implementation.

---

## Token Flow: Step-by-Step

### Step 1: Customer Onboarding

**Nudgebee Dashboard:**
```
Settings → Cloud Accounts → Add Azure Account
  ↓
Generate NudgebeeID: "nbee_abc123xyz..." (stored in cloud_accounts.external_id)
  ↓
Show ARM template deployment link with pre-filled parameters:
  Parameters:
    NudgebeeExternalId: nbee_abc123xyz...
    NudgebeeTenantId: tenant-uuid
    NudgebeeServiceBusEndpoint: sb://nudgebee-events.servicebus.windows.net
```

**Database State:**
```sql
INSERT INTO cloud_accounts (
  id,                  -- 'f3062ba9-...' (UUID)
  account_number,      -- '<azure-tenant-id>' (Azure AD tenant ID)
  tenant,              -- 'e88e208e-...' (UUID)
  external_id,         -- 'nbee_abc123xyz...' (TOKEN)
  assume_role,         -- '<subscription-id>,<subscription-id2>' (comma-separated)
  access_key,          -- '<service-principal-app-id>'
  access_secret,       -- '<encrypted-service-principal-secret>'
  cloud_provider,      -- 'azure'
  ...
) VALUES (...);
```

### Step 2: Customer Deploys ARM Template

**File:** `nudgebee-azure-arm-template.json`

```json
{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "NudgebeeExternalId": {
      "type": "string",
      "minLength": 10,
      "metadata": {
        "description": "Your unique Nudgebee account identifier"
      }
    },
    "NudgebeeTenantId": {
      "type": "string",
      "metadata": {
        "description": "Nudgebee tenant ID"
      }
    },
    "NudgebeeServiceBusEndpoint": {
      "type": "string",
      "defaultValue": "sb://nudgebee-events.servicebus.windows.net/resource-events",
      "metadata": {
        "description": "Nudgebee Service Bus Queue endpoint"
      }
    },
    "EnableEventGridIntegration": {
      "type": "bool",
      "defaultValue": true
    }
  },
  "resources": [
    {
      "type": "Microsoft.EventGrid/systemTopics",
      "apiVersion": "2023-12-15-preview",
      "name": "nudgebee-resource-events",
      "location": "global",
      "properties": {
        "source": "[subscription().id]",
        "topicType": "Microsoft.Resources.Subscriptions"
      }
    },
    {
      "type": "Microsoft.EventGrid/systemTopics/eventSubscriptions",
      "apiVersion": "2023-12-15-preview",
      "name": "nudgebee-resource-events/vm-state-changes",
      "dependsOn": [
        "[resourceId('Microsoft.EventGrid/systemTopics', 'nudgebee-resource-events')]"
      ],
      "properties": {
        "destination": {
          "endpointType": "ServiceBusQueue",
          "properties": {
            "resourceId": "[parameters('NudgebeeServiceBusEndpoint')]"
          }
        },
        "filter": {
          "includedEventTypes": [
            "Microsoft.Resources.ResourceWriteSuccess",
            "Microsoft.Resources.ResourceDeleteSuccess",
            "Microsoft.Resources.ResourceActionSuccess"
          ],
          "advancedFilters": [
            {
              "operatorType": "StringContains",
              "key": "data.resourceProvider",
              "values": [
                "Microsoft.Compute",
                "Microsoft.Sql",
                "Microsoft.Storage",
                "Microsoft.Web",
                "Microsoft.ContainerService",
                "Microsoft.DBforMySQL",
                "Microsoft.DBforPostgreSQL",
                "Microsoft.KeyVault",
                "Microsoft.Network"
              ]
            },
            {
              "operatorType": "StringIn",
              "key": "data.operationName",
              "values": [
                "Microsoft.Compute/virtualMachines/write",
                "Microsoft.Compute/virtualMachines/delete",
                "Microsoft.Compute/virtualMachines/start/action",
                "Microsoft.Compute/virtualMachines/powerOff/action",
                "Microsoft.Compute/virtualMachines/restart/action",
                "Microsoft.Compute/virtualMachines/deallocate/action",
                "Microsoft.Sql/servers/databases/write",
                "Microsoft.Sql/servers/databases/delete",
                "Microsoft.Storage/storageAccounts/write",
                "Microsoft.Storage/storageAccounts/delete",
                "Microsoft.Web/sites/write",
                "Microsoft.Web/sites/delete",
                "Microsoft.Web/sites/start/action",
                "Microsoft.Web/sites/stop/action",
                "Microsoft.ContainerService/managedClusters/write",
                "Microsoft.ContainerService/managedClusters/delete"
              ]
            }
          ]
        },
        "eventDeliverySchema": "EventGridSchema",
        "retryPolicy": {
          "maxDeliveryAttempts": 30,
          "eventTimeToLiveInMinutes": 1440
        },
        "deadLetterDestination": {
          "endpointType": "StorageBlob",
          "properties": {
            "resourceId": "[resourceId('Microsoft.Storage/storageAccounts', 'nudgebee-dlq')]",
            "blobContainerName": "eventgrid-dlq"
          }
        }
      }
    }
  ]
}
```

**CRITICAL DIFFERENCES from AWS EventBridge:**

1. **System Topic**: Uses Azure Resource Manager as the source (all subscription events)
2. **Advanced Filters**: Filter by resource provider and operation name (instead of InputTransformer)
3. **Token Injection**: Cannot add custom properties to Event Grid events directly
   - **Solution**: Add token to Service Bus message properties OR use Azure Functions middleware
4. **Service Bus Authentication**: Uses resource ID (not queue name) in destination

### Step 3: Token Injection Strategy

**Problem**: Azure Event Grid does NOT support custom property injection like AWS InputTransformer.

**Solution Options:**

#### Option A: Azure Functions Middleware (Recommended) ✅

```
Event Grid → Azure Function → Service Bus
             (Injects Token)
```

**ARM Template Addition:**
```json
{
  "type": "Microsoft.Web/sites",
  "apiVersion": "2023-01-01",
  "name": "nudgebee-event-processor",
  "kind": "functionapp",
  "properties": {
    "serverFarmId": "[resourceId('Microsoft.Web/serverfarms', 'nudgebee-plan')]",
    "siteConfig": {
      "appSettings": [
        {
          "name": "NUDGEBEE_EXTERNAL_ID",
          "value": "[parameters('NudgebeeExternalId')]"
        },
        {
          "name": "SERVICE_BUS_CONNECTION_STRING",
          "value": "[parameters('NudgebeeServiceBusConnectionString')]"
        }
      ]
    }
  }
}
```

**Azure Function Code (C#):**
```csharp
[FunctionName("EventGridProcessor")]
public static async Task Run(
    [EventGridTrigger] EventGridEvent eventGridEvent,
    [ServiceBus("resource-events")] IAsyncCollector<ServiceBusMessage> outputMessages,
    ILogger log)
{
    var token = Environment.GetEnvironmentVariable("NUDGEBEE_EXTERNAL_ID");

    // Parse event
    var enrichedEvent = new
    {
        eventGridEvent.Id,
        eventGridEvent.EventType,
        eventGridEvent.Subject,
        eventGridEvent.Data,
        eventGridEvent.EventTime,
        nudgebeeAccountToken = token  // ← TOKEN INJECTED HERE
    };

    var message = new ServiceBusMessage(JsonSerializer.Serialize(enrichedEvent))
    {
        ContentType = "application/json",
        ApplicationProperties =
        {
            ["nudgebeeAccountToken"] = token  // ← ALSO in message properties
        }
    };

    await outputMessages.AddAsync(message);
    log.LogInformation($"Forwarded event {eventGridEvent.Id} with token");
}
```

**Benefits:**
- ✅ Token injected securely (from environment variable)
- ✅ No token in Event Grid subscription (security)
- ✅ Flexible event transformation
- ✅ Can add additional enrichment (tags, metadata)

**Costs:**
- Azure Function (Consumption Plan): ~$0.20 per 1M executions + compute time
- For <10K events/day: <$1/month

#### Option B: Service Bus Message Properties (Direct) ⚠️

Event Grid subscriptions support adding custom headers to Service Bus messages, but this requires the token to be in the ARM template parameters (less secure but simpler).

**ARM Template Event Subscription with Custom Properties:**
```json
{
  "destination": {
    "endpointType": "ServiceBusQueue",
    "properties": {
      "resourceId": "[parameters('NudgebeeServiceBusEndpoint')]",
      "deliveryAttributeMappings": [
        {
          "name": "nudgebeeAccountToken",
          "type": "Static",
          "properties": {
            "value": "[parameters('NudgebeeExternalId')]"
          }
        }
      ]
    }
  }
}
```

**Consumer reads from Application Properties:**
```go
func processServiceBusMessage(msg *azservicebus.ReceivedMessage) {
    token := msg.ApplicationProperties["nudgebeeAccountToken"].(string)
    // ... rest of processing
}
```

**Tradeoff:**
- ✅ Simpler architecture (no Azure Function)
- ⚠️ Token visible in Event Grid subscription configuration
- ❌ Less flexible (no custom transformation)

**Recommendation: Use Option A (Azure Functions) for production deployments.**

---

### Step 4: Runtime Event Flow

**Original Azure Event (VM started):**
```json
{
  "id": "event-123",
  "eventType": "Microsoft.Resources.ResourceWriteSuccess",
  "subject": "/subscriptions/12345678-abcd-ef01-2345-678901234567/resourceGroups/myRG/providers/Microsoft.Compute/virtualMachines/myVM",
  "eventTime": "2025-01-07T10:00:00.000Z",
  "data": {
    "authorization": {
      "action": "Microsoft.Compute/virtualMachines/write",
      "scope": "/subscriptions/.../virtualMachines/myVM"
    },
    "claims": {},
    "correlationId": "corr-123",
    "httpRequest": {},
    "resourceProvider": "Microsoft.Compute",
    "resourceUri": "/subscriptions/.../virtualMachines/myVM",
    "operationName": "Microsoft.Compute/virtualMachines/start/action",
    "status": "Succeeded",
    "subscriptionId": "12345678-abcd-ef01-2345-678901234567",
    "tenantId": "tenant-123"
  },
  "dataVersion": "2.0"
}
```

**After Azure Function Processing (sent to Service Bus):**
```json
{
  "id": "event-123",
  "eventType": "Microsoft.Resources.ResourceWriteSuccess",
  "subject": "/subscriptions/.../virtualMachines/myVM",
  "eventTime": "2025-01-07T10:00:00.000Z",
  "data": {
    "resourceProvider": "Microsoft.Compute",
    "operationName": "Microsoft.Compute/virtualMachines/start/action",
    "status": "Succeeded",
    "resourceUri": "/subscriptions/.../virtualMachines/myVM",
    "subscriptionId": "12345678-abcd-ef01-2345-678901234567",
    "tenantId": "tenant-123",
    "nudgebeeAccountToken": "nbee_abc123xyz..."  ← TOKEN INJECTED!
  }
}
```

### Step 5: Event Processing in cloud-collector

**File:** `providers/azure/event_eventgrid.go`

```go
func processServiceBusMessageForEventGrid(...) {
    // Receive message from Service Bus
    receiver := client.NewReceiverForQueue("resource-events", nil)
    messages, err := receiver.ReceiveMessages(ctx, 10, nil)

    for _, msg := range messages {
        // Parse Event Grid event from message body
        var event EventGridEvent
        json.Unmarshal(msg.Body, &event)

        // Extract token from event data or message properties
        var data map[string]interface{}
        json.Unmarshal(event.Data, &data)

        token := ""
        if t, ok := data["nudgebeeAccountToken"].(string); ok {
            token = t
        } else if t, ok := msg.ApplicationProperties["nudgebeeAccountToken"].(string); ok {
            token = t
        }

        azureTenantId := data["tenantId"].(string)
        subscriptionId := data["subscriptionId"].(string)

        // Lookup account by token
        account := getAccountByExternalId(token, azureTenantId)
        // Returns:
        //   account.Id = "f3062ba9-..." (UUID)
        //   account.TenantId = "e88e208e-..." (UUID)
        //   account.AccountNumber = "<azure-tenant-id>"
        //   account.AssumeRole = "<subscription-id>,<subscription-id2>"

        // Process event with correct tenant context
        processedEvent := processor.Process(ctx, event, account)

        // Update database with tenant isolation
        updateCloudResource(account.Id, account.TenantId, data["resourceUri"], getStatusFromOperation(data["operationName"]))

        // Complete message (removes from queue)
        receiver.CompleteMessage(ctx, msg, nil)
    }
}
```

**Database Lookup:**
```go
func getAccountByExternalId(externalId, azureTenantId string) (Account, error) {
    query := `
        SELECT id, account_number, tenant, cloud_provider, assume_role, ...
        FROM cloud_accounts
        WHERE external_id = $1
          AND account_number = $2
          AND cloud_provider = 'azure'
          AND status = 'active'
    `

    // Returns the CORRECT account even if multiple tenants have same Azure tenant
    return db.QueryOne(query, externalId, azureTenantId)
}
```

**Resource Update:**
```go
func updateCloudResource(accountId, tenantId, resourceId, newStatus string) {
    query := `
        UPDATE cloud_resourses
        SET status = $1,
            last_seen = NOW(),
            is_active = CASE WHEN $1 = 'Deleted' THEN false ELSE true END,
            updated_at = NOW()
        WHERE account = $2      -- UUID (correct account)
          AND tenant = $3       -- UUID (correct tenant)
          AND resourse_id = $4  -- /subscriptions/.../myVM
    `

    db.Exec(query, newStatus, accountId, tenantId, resourceId)
}
```

**Operation Name to Status Mapping:**
```go
var operationStatusMap = map[string]string{
    // VM Operations
    "Microsoft.Compute/virtualMachines/write":      "Active",
    "Microsoft.Compute/virtualMachines/start/action": "Running",
    "Microsoft.Compute/virtualMachines/powerOff/action": "Stopped",
    "Microsoft.Compute/virtualMachines/deallocate/action": "Deallocated",
    "Microsoft.Compute/virtualMachines/restart/action": "Restarting",
    "Microsoft.Compute/virtualMachines/delete":     "Deleted",

    // SQL Operations
    "Microsoft.Sql/servers/databases/write":        "Active",
    "Microsoft.Sql/servers/databases/delete":       "Deleted",
    "Microsoft.Sql/servers/databases/pause/action": "Paused",
    "Microsoft.Sql/servers/databases/resume/action": "Running",

    // Storage Operations
    "Microsoft.Storage/storageAccounts/write":      "Active",
    "Microsoft.Storage/storageAccounts/delete":     "Deleted",

    // Web App Operations
    "Microsoft.Web/sites/write":                    "Active",
    "Microsoft.Web/sites/start/action":            "Running",
    "Microsoft.Web/sites/stop/action":             "Stopped",
    "Microsoft.Web/sites/delete":                   "Deleted",

    // AKS Operations
    "Microsoft.ContainerService/managedClusters/write": "Active",
    "Microsoft.ContainerService/managedClusters/start/action": "Running",
    "Microsoft.ContainerService/managedClusters/stop/action": "Stopped",
    "Microsoft.ContainerService/managedClusters/delete": "Deleted",
}
```

---

## Database Schema

### cloud_accounts Table

```sql
CREATE TABLE cloud_accounts (
    id UUID PRIMARY KEY,
    account_number TEXT,           -- Azure AD Tenant ID
    tenant UUID REFERENCES tenant(id),
    external_id TEXT,              -- TOKEN: "nbee_abc123xyz..."
    assume_role TEXT,              -- Subscription ID(s) comma-separated
    access_key TEXT,               -- Service Principal App ID
    access_secret TEXT,            -- Encrypted Service Principal Secret
    cloud_provider TEXT,           -- 'azure'
    status TEXT,
    ...
);

CREATE INDEX cloud_accounts_external_id ON cloud_accounts(external_id);
CREATE INDEX cloud_accounts_tenant ON cloud_accounts(tenant);
CREATE INDEX cloud_accounts_provider_status ON cloud_accounts(cloud_provider, status);
```

**Key Constraint:**
- `external_id` is unique per account
- Multiple accounts can have same `account_number` (different tenants sharing same Azure AD)
- Token (`external_id`) provides tenant-safe lookup

### cloud_resourses Table

```sql
CREATE TABLE cloud_resourses (
    id UUID PRIMARY KEY,
    account UUID REFERENCES cloud_accounts(id),  -- UUID (not subscription ID!)
    tenant UUID REFERENCES tenant(id),
    resourse_id TEXT,              -- /subscriptions/.../resourceGroups/.../providers/.../resourceName
    service_name TEXT,             -- microsoft.compute/virtualmachines, etc.
    type TEXT,                     -- virtualmachine, database, storageaccount, etc.
    status TEXT,                   -- Active, Running, Stopped, Deleted, etc.
    region TEXT,                   -- eastus, westus2, etc.
    last_seen TIMESTAMP,
    is_active BOOLEAN,
    meta JSONB,
    tags JSONB,
    ...
);

CREATE UNIQUE INDEX ON cloud_resourses(account, external_resource_id);
CREATE INDEX ON cloud_resourses(tenant, account);
CREATE INDEX ON cloud_resourses(service_name, status);
```

---

## Infrastructure Setup

### Nudgebee Azure Subscription Setup

**ARM Template:** `deploy/azure/nudgebee-eventgrid-infrastructure.json`

```json
{
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
  "contentVersion": "1.0.0.0",
  "resources": [
    {
      "type": "Microsoft.ServiceBus/namespaces",
      "apiVersion": "2023-01-01-preview",
      "name": "nudgebee-events",
      "location": "[resourceGroup().location]",
      "sku": {
        "name": "Standard",
        "tier": "Standard"
      },
      "properties": {}
    },
    {
      "type": "Microsoft.ServiceBus/namespaces/queues",
      "apiVersion": "2023-01-01-preview",
      "name": "nudgebee-events/resource-events",
      "dependsOn": [
        "[resourceId('Microsoft.ServiceBus/namespaces', 'nudgebee-events')]"
      ],
      "properties": {
        "lockDuration": "PT5M",
        "maxSizeInMegabytes": 5120,
        "requiresDuplicateDetection": true,
        "duplicateDetectionHistoryTimeWindow": "PT10M",
        "requiresSession": false,
        "defaultMessageTimeToLive": "P14D",
        "deadLetteringOnMessageExpiration": true,
        "maxDeliveryCount": 3,
        "enablePartitioning": false,
        "enableExpress": false
      }
    },
    {
      "type": "Microsoft.ServiceBus/namespaces/queues",
      "apiVersion": "2023-01-01-preview",
      "name": "nudgebee-events/resource-events-dlq",
      "dependsOn": [
        "[resourceId('Microsoft.ServiceBus/namespaces', 'nudgebee-events')]"
      ],
      "properties": {
        "lockDuration": "PT5M",
        "maxSizeInMegabytes": 1024,
        "requiresDuplicateDetection": false,
        "requiresSession": false,
        "defaultMessageTimeToLive": "P14D",
        "maxDeliveryCount": 10,
        "enablePartitioning": false
      }
    },
    {
      "type": "Microsoft.ServiceBus/namespaces/AuthorizationRules",
      "apiVersion": "2023-01-01-preview",
      "name": "nudgebee-events/EventGridSendPolicy",
      "dependsOn": [
        "[resourceId('Microsoft.ServiceBus/namespaces', 'nudgebee-events')]"
      ],
      "properties": {
        "rights": ["Send"]
      }
    },
    {
      "type": "Microsoft.ServiceBus/namespaces/AuthorizationRules",
      "apiVersion": "2023-01-01-preview",
      "name": "nudgebee-events/CollectorListenPolicy",
      "dependsOn": [
        "[resourceId('Microsoft.ServiceBus/namespaces', 'nudgebee-events')]"
      ],
      "properties": {
        "rights": ["Listen", "Manage"]
      }
    }
  ],
  "outputs": {
    "serviceBusEndpoint": {
      "type": "string",
      "value": "[concat('sb://', reference(resourceId('Microsoft.ServiceBus/namespaces', 'nudgebee-events')).serviceBusEndpoint)]"
    },
    "queueName": {
      "type": "string",
      "value": "resource-events"
    }
  }
}
```

**Deploy:**
```bash
az deployment group create \
  --resource-group nudgebee-infra \
  --template-file nudgebee-eventgrid-infrastructure.json \
  --name nudgebee-eventgrid-infra
```

### Configuration Update

**File:** `config/config.go`

```go
type Config struct {
    // Existing
    CloudCollectorAzureServiceBusConnectionString string `env:"CLOUD_COLLECTOR_AZURE_SERVICE_BUS_CONNECTION_STRING"`
    CloudCollectorAzureServiceBusQueueName        string `env:"CLOUD_COLLECTOR_AZURE_SERVICE_BUS_QUEUE_NAME"`

    // OR using Azure Identity (Managed Identity/Service Principal)
    CloudCollectorAzureServiceBusNamespace string `env:"CLOUD_COLLECTOR_AZURE_SERVICE_BUS_NAMESPACE"`
}
```

**Environment:**
```bash
# Option 1: Connection String (simpler for development)
CLOUD_COLLECTOR_AZURE_SERVICE_BUS_CONNECTION_STRING="Endpoint=sb://nudgebee-events.servicebus.windows.net/;SharedAccessKeyName=CollectorListenPolicy;SharedAccessKey=<key>"
CLOUD_COLLECTOR_AZURE_SERVICE_BUS_QUEUE_NAME="resource-events"

# Option 2: Managed Identity (recommended for production)
CLOUD_COLLECTOR_AZURE_SERVICE_BUS_NAMESPACE="nudgebee-events.servicebus.windows.net"
# Uses DefaultAzureCredential (Managed Identity, Service Principal, etc.)
```

---

## Security Model

### Token-Based Authentication

**Security Properties:**
1. **Token is secret:** Only in customer ARM template parameters and Nudgebee DB
2. **Token proves identity:** Maps to specific tenant + account
3. **Token rotation:** Can regenerate if compromised
4. **Multi-factor validation:** Token + Azure Tenant ID must both match

### Threat Model

| Attack Scenario | Mitigation |
|----------------|------------|
| **Stolen Token** | Token alone insufficient - also requires matching Azure Tenant ID |
| **Cross-tenant Access** | Token uniquely identifies tenant - DB query enforces isolation |
| **Token Guessing** | 160-bit random token (base32) = ~10^48 possibilities |
| **Replay Attacks** | Events are idempotent - duplicate updates safe; Service Bus duplicate detection |
| **Man-in-the-Middle** | All communication over TLS; events stay within Azure |
| **Service Bus Access** | Separate policies for Event Grid (Send) and Collector (Listen) |

### Azure RBAC Permissions

**Customer Subscription:**
- Service Principal needs: `Event Grid Contributor` on subscription (to create system topics)
- ARM template deployment requires: `Contributor` role

**Nudgebee Subscription:**
- Service Bus namespace:
  - `EventGridSendPolicy`: Send only (used by Event Grid subscriptions)
  - `CollectorListenPolicy`: Listen + Manage (used by cloud-collector)
- Managed Identity for cloud-collector: `Azure Service Bus Data Receiver` role

**Azure Function (if using middleware):**
- `Event Grid Data Sender` (to receive from Event Grid)
- `Azure Service Bus Data Sender` (to send to Service Bus)

---

## Event Processing Rules

### Example: VM State Change

**File:** `providers/azure/azure_resource_events.yaml`

```yaml
rules:
  - name: azure_vm_state_change
    triggers:
      source: Azure_EventGrid
      alert_name: azure.resource.compute.vm
      event_filters:
        - template: '{{ eq .EventType "Microsoft.Resources.ResourceWriteSuccess" }}'
        - template: '{{ contains .Data.operationName "Microsoft.Compute/virtualMachines" }}'

    event_template:
      title:
        template: 'VM {{ .Data.resourceUri | basename }} → {{ .Data.status }}'
      severity: Info
      resource_id:
        template: '{{ .Data.resourceUri }}'
      resource_service_name:
        value: "microsoft.compute/virtualmachines"

    actions:
      - name: update_resource_state
        type: update_cloud_resource
        params:
          resource_id: '{{ .Data.resourceUri }}'
          service_name: 'microsoft.compute/virtualmachines'
          region: '{{ extractRegion .Data.resourceUri }}'
          operation_mapping:
            "Microsoft.Compute/virtualMachines/write": Active
            "Microsoft.Compute/virtualMachines/start/action": Running
            "Microsoft.Compute/virtualMachines/powerOff/action": Stopped
            "Microsoft.Compute/virtualMachines/deallocate/action": Deallocated
            "Microsoft.Compute/virtualMachines/restart/action": Restarting
            "Microsoft.Compute/virtualMachines/delete": Deleted
          new_status: '{{ index .operationMapping .Data.operationName }}'
          update_last_seen: true
          update_meta: true
          meta_updates:
            last_operation: '{{ .Data.operationName }}'
            last_operation_time: '{{ .EventTime }}'
            operation_status: '{{ .Data.status }}'
```

### Action Handler: update_cloud_resource

**File:** `providers/azure/event_eventgrid_processor.go`

```go
type UpdateCloudResourceActionParams struct {
    ResourceId        string            `json:"resource_id"`
    ServiceName       string            `json:"service_name"`
    Region            string            `json:"region"`
    NewStatus         string            `json:"new_status"`
    OperationMapping  map[string]string `json:"operation_mapping"`
    UpdateLastSeen    bool              `json:"update_last_seen"`
    UpdateMeta        bool              `json:"update_meta"`
    MetaUpdates       map[string]any    `json:"meta_updates"`
}

func (p *TemplatedEventGridProcessor) updateCloudResource(
    ctx providers.CloudProviderContext,
    account providers.Account,  // Has account.Id (UUID) and account.TenantId (UUID)
    params UpdateCloudResourceActionParams,
) (any, error) {
    dbms := common.GetDatabaseManager(common.Metastore)

    updates := []string{}
    args := []any{}

    if params.UpdateLastSeen {
        updates = append(updates, "last_seen = $1")
        args = append(args, time.Now().UTC())
    }

    if params.NewStatus != "" {
        updates = append(updates, "status = $2")
        args = append(args, params.NewStatus)

        updates = append(updates, "is_active = $3")
        args = append(args, params.NewStatus != "Deleted")
    }

    if params.UpdateMeta {
        updates = append(updates, "meta = meta || $4::jsonb")
        metaJson, _ := json.Marshal(params.MetaUpdates)
        args = append(args, string(metaJson))
    }

    // CRITICAL: Use account UUID and tenant UUID for isolation
    args = append(args, account.Id, account.TenantId, params.ResourceId, params.ServiceName, params.Region)

    query := fmt.Sprintf(`
        UPDATE cloud_resourses
        SET %s, updated_at = NOW()
        WHERE account = $5      -- UUID from token lookup
          AND tenant = $6       -- Tenant isolation!
          AND resourse_id = $7
          AND lower(service_name) = lower($8)
          AND region = $9
    `, strings.Join(updates, ", "))

    result, err := dbms.Exec(query, args...)
    rowsAffected, _ := result.RowsAffected()

    return map[string]any{
        "rows_affected": rowsAffected,
        "resource_id":   params.ResourceId,
        "status":        params.NewStatus,
    }, err
}
```

---

## Service Bus Consumer Implementation

**File:** `providers/azure/event_service_bus_consumer.go`

```go
package azure

import (
    "context"
    "encoding/json"
    "fmt"
    "time"

    "github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
    "github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

type ServiceBusConsumer struct {
    client   *azservicebus.Client
    receiver *azservicebus.Receiver
    config   ServiceBusConfig
}

type ServiceBusConfig struct {
    ConnectionString string
    Namespace        string
    QueueName        string
}

func NewServiceBusConsumer(config ServiceBusConfig) (*ServiceBusConsumer, error) {
    var client *azservicebus.Client
    var err error

    if config.ConnectionString != "" {
        // Option 1: Connection String
        client, err = azservicebus.NewClientFromConnectionString(config.ConnectionString, nil)
    } else {
        // Option 2: Managed Identity / Default Credentials
        cred, err := azidentity.NewDefaultAzureCredential(nil)
        if err != nil {
            return nil, fmt.Errorf("failed to create credential: %w", err)
        }
        client, err = azservicebus.NewClient(config.Namespace, cred, nil)
    }

    if err != nil {
        return nil, fmt.Errorf("failed to create Service Bus client: %w", err)
    }

    receiver, err := client.NewReceiverForQueue(config.QueueName, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create receiver: %w", err)
    }

    return &ServiceBusConsumer{
        client:   client,
        receiver: receiver,
        config:   config,
    }, nil
}

func (c *ServiceBusConsumer) Start(ctx context.Context) error {
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        default:
            if err := c.processMessages(ctx); err != nil {
                log.Error("Failed to process messages", "error", err)
                time.Sleep(5 * time.Second)
            }
        }
    }
}

func (c *ServiceBusConsumer) processMessages(ctx context.Context) error {
    // Receive up to 10 messages with 20 second timeout
    messages, err := c.receiver.ReceiveMessages(ctx, 10, &azservicebus.ReceiveMessagesOptions{
        MaxWaitTime: 20 * time.Second,
    })

    if err != nil {
        return fmt.Errorf("failed to receive messages: %w", err)
    }

    for _, msg := range messages {
        if err := c.processMessage(ctx, msg); err != nil {
            log.Error("Failed to process message",
                "messageId", msg.MessageID,
                "error", err)

            // Dead letter the message after max retries
            if msg.DeliveryCount >= 3 {
                c.receiver.DeadLetterMessage(ctx, msg, &azservicebus.DeadLetterOptions{
                    ErrorDescription: to.Ptr(err.Error()),
                    Reason:          to.Ptr("ProcessingFailed"),
                })
            } else {
                // Abandon to retry
                c.receiver.AbandonMessage(ctx, msg, nil)
            }
        } else {
            // Complete (delete) message
            c.receiver.CompleteMessage(ctx, msg, nil)
        }
    }

    return nil
}

func (c *ServiceBusConsumer) processMessage(ctx context.Context, msg *azservicebus.ReceivedMessage) error {
    // Parse Event Grid event from message body
    var event EventGridEvent
    if err := json.Unmarshal(msg.Body, &event); err != nil {
        return fmt.Errorf("failed to parse event: %w", err)
    }

    // Extract token from event data or application properties
    token := ""

    // Try from event data first (Azure Function enriched)
    var data map[string]interface{}
    if err := json.Unmarshal(event.Data, &data); err == nil {
        if t, ok := data["nudgebeeAccountToken"].(string); ok {
            token = t
        }
    }

    // Fallback to application properties (direct Event Grid delivery)
    if token == "" {
        if msg.ApplicationProperties != nil {
            if t, ok := msg.ApplicationProperties["nudgebeeAccountToken"].(string); ok {
                token = t
            }
        }
    }

    if token == "" {
        return fmt.Errorf("nudgebeeAccountToken not found in event")
    }

    // Extract Azure identifiers
    azureTenantId := ""
    subscriptionId := ""

    if tid, ok := data["tenantId"].(string); ok {
        azureTenantId = tid
    }
    if sid, ok := data["subscriptionId"].(string); ok {
        subscriptionId = sid
    }

    // Lookup account by token
    account, err := getAccountByExternalId(token, azureTenantId)
    if err != nil {
        return fmt.Errorf("failed to lookup account: %w", err)
    }

    // Process event with templated processor
    processor := NewTemplatedEventGridProcessor()
    processedEvent, err := processor.Process(ctx, event, account)
    if err != nil {
        return fmt.Errorf("failed to process event: %w", err)
    }

    log.Info("Processed Event Grid event",
        "eventId", event.Id,
        "eventType", event.EventType,
        "accountId", account.Id,
        "tenantId", account.TenantId,
        "resourceId", data["resourceUri"])

    return nil
}

func (c *ServiceBusConsumer) Close() error {
    if c.receiver != nil {
        c.receiver.Close(context.Background())
    }
    if c.client != nil {
        c.client.Close(context.Background())
    }
    return nil
}
```

**Integration in main.go:**

```go
// In collector-server/cloud-collector/cmd/main.go

func startAzureServiceBusConsumer(ctx context.Context) {
    config := ServiceBusConfig{
        ConnectionString: os.Getenv("CLOUD_COLLECTOR_AZURE_SERVICE_BUS_CONNECTION_STRING"),
        Namespace:        os.Getenv("CLOUD_COLLECTOR_AZURE_SERVICE_BUS_NAMESPACE"),
        QueueName:        getEnvOrDefault("CLOUD_COLLECTOR_AZURE_SERVICE_BUS_QUEUE_NAME", "resource-events"),
    }

    consumer, err := azure.NewServiceBusConsumer(config)
    if err != nil {
        log.Fatal("Failed to create Service Bus consumer", "error", err)
    }

    go func() {
        log.Info("Starting Azure Service Bus consumer")
        if err := consumer.Start(ctx); err != nil {
            log.Error("Service Bus consumer error", "error", err)
        }
    }()
}
```

---

## Migration & Rollout

### Phase 1: Infrastructure Setup (Nudgebee Side)

1. **Deploy Service Bus infrastructure in Azure:**
   ```bash
   az group create --name nudgebee-infra --location eastus
   az deployment group create \
     --resource-group nudgebee-infra \
     --template-file nudgebee-eventgrid-infrastructure.json \
     --name nudgebee-eventgrid-infra
   ```

2. **Retrieve connection strings:**
   ```bash
   az servicebus namespace authorization-rule keys list \
     --resource-group nudgebee-infra \
     --namespace-name nudgebee-events \
     --name CollectorListenPolicy \
     --query primaryConnectionString -o tsv
   ```

3. **Update cloud-collector config:**
   ```bash
   export CLOUD_COLLECTOR_AZURE_SERVICE_BUS_CONNECTION_STRING="Endpoint=sb://..."
   export CLOUD_COLLECTOR_AZURE_SERVICE_BUS_QUEUE_NAME="resource-events"
   ```

4. **Deploy code changes:**
   - Event processor with token lookup
   - Service Bus consumer
   - Resource update action handler
   - Event rules for resource sync

### Phase 2: ARM Template Creation

1. **Create customer ARM template:**
   - System topic for subscription events
   - Event subscriptions with filters
   - Azure Function for token injection (recommended)
   - OR direct Service Bus delivery with static token

2. **Test with new customer account:**
   - Deploy ARM template
   - Trigger resource events (start/stop VM)
   - Verify events flow end-to-end
   - Validate tenant isolation

### Phase 3: Existing Customer Migration

**Option A: Opt-in (Safe):**
1. Existing customers keep current periodic polling
2. New parameter `EnableEventGridIntegration=true` (default: false)
3. Customers opt-in via ARM template deployment when ready

**Option B: Gradual Rollout:**
1. Update 1-2 beta customers
2. Monitor for 1 week
3. Offer to all customers

### Phase 4: Token Management

**For new accounts:**
```go
func CreateCloudAccount(...) {
    token := generateSecureToken()  // nbee_XXXXX

    account := CloudAccount{
        Id: uuid.New(),
        ExternalId: token,  // Store token
        AccountNumber: azureTenantId,
        AssumeRole: subscriptionId,  // Can be comma-separated
        Tenant: tenantId,
        CloudProvider: "azure",
        ...
    }

    db.Insert(account)

    // Return token to customer for ARM template
    return token
}
```

**For existing accounts (migration):**
```go
func GenerateTokenForExistingAccount(accountId string) string {
    account := db.GetAccount(accountId)

    if account.ExternalId != "" && strings.HasPrefix(account.ExternalId, "nbee_") {
        return account.ExternalId  // Already has token
    }

    token := generateSecureToken()
    db.Exec("UPDATE cloud_accounts SET external_id = $1 WHERE id = $2", token, accountId)

    return token
}
```

---

## Monitoring & Observability

### Metrics to Track

1. **Event Grid Metrics:**
   - Matched events (should match operation filters)
   - Unmatched events (indicates filter issues)
   - Publish success/failure rate
   - Delivery success/failure rate
   - Dead lettered events

2. **Service Bus Metrics:**
   - Active messages in queue
   - Dead letter queue message count
   - Incoming/outgoing messages per second
   - Server errors
   - Throttled requests

3. **Azure Function Metrics (if using):**
   - Execution count
   - Execution duration
   - Errors
   - HTTP 5xx errors

4. **Application Metrics:**
   - Events processed per second
   - Token lookup failures
   - Resource update success/failure rate
   - Tenant isolation violations (should be zero!)
   - Message processing latency

### Azure Monitor Alerts

```json
{
  "type": "Microsoft.Insights/metricAlerts",
  "apiVersion": "2018-03-01",
  "name": "ServiceBusDeadLetterAlert",
  "properties": {
    "description": "Alert when messages appear in DLQ",
    "severity": 2,
    "enabled": true,
    "scopes": [
      "[resourceId('Microsoft.ServiceBus/namespaces', 'nudgebee-events')]"
    ],
    "evaluationFrequency": "PT5M",
    "windowSize": "PT5M",
    "criteria": {
      "allOf": [
        {
          "criterionType": "StaticThresholdCriterion",
          "name": "DeadLetterCount",
          "metricName": "DeadletteredMessages",
          "metricNamespace": "Microsoft.ServiceBus/namespaces",
          "dimensions": [
            {
              "name": "EntityName",
              "operator": "Include",
              "values": ["resource-events"]
            }
          ],
          "operator": "GreaterThan",
          "threshold": 10,
          "timeAggregation": "Average"
        }
      ]
    },
    "actions": [
      {
        "actionGroupId": "[resourceId('Microsoft.Insights/actionGroups', 'nudgebee-alerts')]"
      }
    ]
  }
}
```

### Logging

```go
logger.Info("Processing Event Grid event",
    "eventId", event.Id,
    "eventType", event.EventType,
    "subject", event.Subject,
    "token", maskToken(token),  // Log masked: "nbee_***xyz"
    "azureTenantId", azureTenantId,
    "subscriptionId", subscriptionId,
    "resolvedAccountId", account.Id,
    "resolvedTenant", account.TenantId,
    "processingTime", time.Since(startTime))
```

---

## Testing Plan

### Unit Tests

```go
func TestGetAccountByExternalId(t *testing.T) {
    // Setup: Create multiple accounts with same subscription, different tenants
    account1 := createTestAccount(tenant1, "tenant-id-123", "sub-id-123", "nbee_token1")
    account2 := createTestAccount(tenant2, "tenant-id-123", "sub-id-123", "nbee_token2")

    // Test: Lookup by token1
    result := getAccountByExternalId("nbee_token1", "tenant-id-123")
    assert.Equal(t, account1.Id, result.Id)
    assert.Equal(t, tenant1, result.TenantId)

    // Test: Lookup by token2
    result = getAccountByExternalId("nbee_token2", "tenant-id-123")
    assert.Equal(t, account2.Id, result.Id)
    assert.Equal(t, tenant2, result.TenantId)
}
```

### Integration Tests

1. **Deploy test ARM template**
2. **Send test event to Service Bus:**
   ```bash
   az servicebus queue send \
     --resource-group nudgebee-infra \
     --namespace-name nudgebee-events \
     --name resource-events \
     --body '{
       "id": "event-test-123",
       "eventType": "Microsoft.Resources.ResourceWriteSuccess",
       "subject": "/subscriptions/.../virtualMachines/test-vm",
       "data": {
         "operationName": "Microsoft.Compute/virtualMachines/start/action",
         "status": "Succeeded",
         "resourceUri": "/subscriptions/.../test-vm",
         "tenantId": "tenant-123",
         "subscriptionId": "sub-123",
         "nudgebeeAccountToken": "nbee_test_token"
       }
     }'
   ```
3. **Verify `cloud_resourses` updated**
4. **Verify correct tenant isolation**

### End-to-End Test

1. Deploy full ARM template in test Azure subscription
2. Start/stop Azure VM
3. Verify event received in ~30s (Event Grid delivery SLA)
4. Check database updated with correct tenant/account
5. Validate metrics and logs

---

## Troubleshooting

### Common Issues

| Issue | Cause | Solution |
|-------|-------|----------|
| Events not received | Event Grid subscription misconfigured | Check filters, verify Service Bus endpoint |
| Token not found | Azure Function not injecting token | Check function logs, verify environment variable |
| Token lookup fails | Token not in database | Verify `external_id` populated |
| Wrong tenant data | Token reused across tenants | Tokens must be unique per account |
| Messages in DLQ | Event parsing errors | Check event schema matches processor |
| Resource not updated | Account UUID mismatch | Verify token maps to correct account.Id |
| Function errors | Permissions missing | Check Managed Identity role assignments |

### Debug Commands

```bash
# Check Event Grid system topic
az eventgrid system-topic show \
  --name nudgebee-resource-events \
  --resource-group <customer-rg>

# Check Event Grid subscription
az eventgrid system-topic event-subscription show \
  --name vm-state-changes \
  --system-topic-name nudgebee-resource-events \
  --resource-group <customer-rg>

# Check Service Bus messages
az servicebus queue peek \
  --resource-group nudgebee-infra \
  --namespace-name nudgebee-events \
  --name resource-events \
  --max-count 1

# Check DLQ
az servicebus queue show \
  --resource-group nudgebee-infra \
  --namespace-name nudgebee-events \
  --name resource-events-dlq \
  --query countDetails

# Database debug
SELECT external_id, account_number, assume_role, tenant
FROM cloud_accounts
WHERE external_id LIKE 'nbee_%' AND cloud_provider = 'azure';
```

---

## Appendix

### Token Generation Algorithm

```go
func generateSecureToken() string {
    // 20 bytes = 160 bits of entropy
    b := make([]byte, 20)
    rand.Read(b)

    // Base32 encoding (URL-safe, case-insensitive)
    encoded := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)

    // Format: nbee_XXXXX (40 chars total)
    return fmt.Sprintf("nbee_%s", strings.ToLower(encoded))
}
```

**Example tokens:**
- `nbee_7q2m3n5p8r9s4t6v7w8x9y2a3b4c5d6e7f8g`
- `nbee_a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7r8`

**Security:**
- 160 bits = 2^160 = ~1.46 × 10^48 possible tokens
- Collision probability: Negligible even with millions of accounts
- Guessing probability: Astronomically low

### Resource ID Parsing

Azure resource IDs follow this format:
```
/subscriptions/{subscription-id}/resourceGroups/{rg-name}/providers/{namespace}/{type}/{name}
```

**Helper functions:**

```go
func extractSubscriptionId(resourceId string) string {
    parts := strings.Split(resourceId, "/")
    for i, part := range parts {
        if part == "subscriptions" && i+1 < len(parts) {
            return parts[i+1]
        }
    }
    return ""
}

func extractResourceGroup(resourceId string) string {
    parts := strings.Split(resourceId, "/")
    for i, part := range parts {
        if part == "resourceGroups" && i+1 < len(parts) {
            return parts[i+1]
        }
    }
    return ""
}

func extractRegion(resourceId string, meta map[string]any) string {
    // Azure resource IDs don't contain region, must get from event metadata
    if location, ok := meta["location"].(string); ok {
        return normalizeAzureRegion(location)
    }
    return ""
}

func extractResourceName(resourceId string) string {
    parts := strings.Split(resourceId, "/")
    if len(parts) > 0 {
        return parts[len(parts)-1]
    }
    return ""
}
```

### Related Files

- **ARM Template (customer):** `resources/azure_resources/nudgebee-azure-arm-template.json` (to be created)
- **ARM Template (Nudgebee):** `resources/azure_resources/nudgebee-eventgrid-infrastructure.json` (to be created)
- **Azure Function:** `resources/azure_resources/event-processor-function/` (to be created)
- **Event Consumer:** `providers/azure/event_service_bus_consumer.go` (to be created)
- **Event Processor:** `providers/azure/event_eventgrid_processor.go` (to be created)
- **Event Rules:** `providers/azure/azure_resource_events.yaml` (to be created)
- **Database:** Schema in `api-server/hasura/`
- **Configuration:** `config/config.go`

---

## Implementation Checklist

### Phase 1: Nudgebee Infrastructure
- [ ] Create ARM template for Service Bus namespace and queues
- [ ] Deploy Service Bus in Nudgebee Azure subscription
- [ ] Configure access policies (Send for Event Grid, Listen for collector)
- [ ] Set up monitoring and alerts
- [ ] Test Service Bus connectivity

### Phase 2: Event Processing Code
- [ ] Implement Service Bus consumer (`event_service_bus_consumer.go`)
- [ ] Add token extraction logic
- [ ] Implement account lookup by `external_id`
- [ ] Create Event Grid event processor (`event_eventgrid_processor.go`)
- [ ] Define resource update action handlers
- [ ] Write unit tests
- [ ] Integration tests with mock Service Bus

### Phase 3: Customer ARM Template
- [ ] Create customer ARM template with System Topic
- [ ] Add Event Grid subscriptions with filters
- [ ] Decide: Azure Function middleware OR direct delivery
- [ ] If Azure Function: Create function app code
- [ ] If Azure Function: Add function deployment to ARM template
- [ ] Test token injection mechanism
- [ ] Document deployment process

### Phase 4: Configuration & Deployment
- [ ] Add Service Bus config to `config.go`
- [ ] Update environment variable documentation
- [ ] Configure connection string/Managed Identity
- [ ] Deploy collector with Service Bus consumer
- [ ] Smoke test with test subscription

### Phase 5: Event Rules
- [ ] Define event rules in `azure_resource_events.yaml`
- [ ] VM state changes
- [ ] SQL database operations
- [ ] Storage account operations
- [ ] Web App operations
- [ ] AKS cluster operations
- [ ] Test each rule type

### Phase 6: Dashboard Integration
- [ ] Add "Enable Real-Time Updates" option in dashboard
- [ ] Generate token on account creation
- [ ] Display ARM template deployment link
- [ ] Show deployment status
- [ ] Monitor event flow in UI

### Phase 7: Testing & Validation
- [ ] End-to-end test with test subscription
- [ ] Multi-tenant isolation test
- [ ] Performance test (1000s of events)
- [ ] Failover test (DLQ behavior)
- [ ] Token rotation test
- [ ] Documentation review

### Phase 8: Rollout
- [ ] Beta test with 1-2 customers
- [ ] Monitor for 1 week
- [ ] Gather feedback
- [ ] Fix issues
- [ ] General availability announcement
- [ ] Update documentation

---

**Document Version:** 1.0
**Last Updated:** 2026-01-07
**Author:** Nudgebee Engineering
**Status:** Design Document - Implementation Required

---

## Summary: Key Differences from AWS

| Aspect | AWS EventBridge | Azure Event Grid |
|--------|----------------|------------------|
| **Event Source** | EventBridge Rules on resource events | System Topic for subscription events |
| **Message Queue** | SQS Queue | Service Bus Queue |
| **Token Injection** | InputTransformer (native) | Azure Function OR message properties |
| **Filtering** | Event pattern matching | Advanced filters on resource provider & operation |
| **Authentication** | IAM roles & policies | Service Principal + Managed Identity |
| **Delivery Guarantee** | At-least-once | At-least-once with duplicate detection |
| **Dead Letter** | SQS DLQ | Service Bus DLQ (built-in) |
| **Retry Logic** | SQS visibility timeout | Service Bus message lock + delivery count |
| **Cost** | Low (EventBridge + SQS) | Medium (Event Grid + Service Bus + optional Function) |
| **Latency** | ~10-30 seconds | ~10-30 seconds (+ Function processing if used) |

**Recommendation for Implementation:**
- Use **Azure Function middleware** for token injection (more secure, flexible)
- Use **Service Bus Standard tier** (sufficient for <10K events/day)
- Enable **duplicate detection** (10-minute window)
- Configure **dead-lettering** with 3 max delivery attempts
- Use **Managed Identity** for cloud-collector authentication (avoid connection strings in production)
