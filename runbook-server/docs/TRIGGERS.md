# Workflow Triggers

Workflows can be triggered by various events. The `triggers` section in the workflow definition specifies one or more triggers. Each trigger type has its own configuration parameters.

## Supported Triggers

### 1. Schedule Trigger (`schedule`)

Runs the workflow automatically based on a defined schedule (Cron).

#### Parameters (`params`)

*   **`cron`** (string, required): A standard cron expression defining the schedule.
    *   Example: `"0 * * * *"` (Every hour)
    *   Example: `"*/5 * * * *"` (Every 5 minutes)
*   **`overlap_policy`** (string, optional): Defines the behavior when a new schedule run is due while a previous run of the same schedule is still active.
    *   **Supported Values:**
        *   `Skip` (Default): If a run is in progress, the next scheduled run is skipped.
        *   `BufferOne`: The next run is queued and waits for the current one to finish.
        *   `BufferAll`: All missed runs are queued and will execute sequentially.
        *   `AllowAll`: The next run starts immediately, allowing parallel executions.
        *   `CancelOther`: The currently running workflow is canceled, and the new run starts.
        *   `TerminateOther`: The currently running workflow is terminated, and the new run starts.
*   **`catchup_window`** (string, optional): A duration string defining how far back the system looks for missed schedule runs after an outage or pause.
    *   **Format:** Go duration string (e.g., `"10m"`, `"1h"`, `"24h"`).
    *   **Default:** `"60s"`.

#### Example

```yaml
triggers:
  - type: schedule
    params:
      cron: "0 9 * * Mon-Fri" # 9 AM on weekdays
      overlap_policy: BufferOne
      catchup_window: 1h
```

---

### 2. Webhook Trigger (`webhook`)

Runs the workflow when an external HTTP request is received.

#### Parameters (`params`)

*   **`integration_name`** (string, required): The unique identifier of the integration configuration that validates and routes this webhook.

#### Example

```yaml
triggers:
  - type: webhook
          params:
            integration_name: "int-12345-github"
    ```
    
    ## Automatic Context Variables
    
    Every workflow execution automatically includes a set of context variables in the `inputs` map. These can be accessed in your tasks using the `Inputs` prefix (e.g., `{{ Inputs.workflow_execution_time }}`).
    
    | Variable | Description | Format |
    | :--- | :--- | :--- |
    | `workflow_execution_time` | The actual time the current execution started. | RFC3339 |
    | `workflow_scheduled_time` | The intended time the workflow was supposed to run (logical time). | RFC3339 |
    | `workflow_last_execution_time` | The start time of the **previous successful** run of this workflow. | RFC3339 |
    | `workflow_execution_id` | The unique ID (Run ID) of the current execution. | UUID |
    | `workflow_id` | The ID of the workflow definition. | UUID |
    | `workflow_name` | The name of the workflow. | String |
    
    > **Note:** `workflow_last_execution_time` is persisted in the workflow's state and will be empty on the first run. it is only updated upon successful completion of a run.
    ---

### 3. Manual Trigger (`manual`)

Allows the workflow to be triggered manually via the API or CLI. This trigger type accepts no parameters.

#### Example

```yaml
triggers:
  - type: manual
```
