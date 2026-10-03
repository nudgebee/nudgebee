/**
 * Share / export helpers for the Automation listing's row menu.
 *
 * Kept pure (no React, no network) so the sanitisation rules are unit-testable
 * and shared between "Copy JSON" and "Duplicate" — both send a workflow
 * definition somewhere it can be re-imported, so both must strip the same
 * system-managed fields.
 */
import type { WorkflowRequest } from '@api1/workflow/types';
import { workflowTagsToMap } from './utils/workflowApiUtils';

/**
 * Deep-clone a workflow definition and strip fields that must not travel with
 * an exported / duplicated copy:
 *  - task `outputs` — run artifacts, rejected by WorkflowDefinitionTaskRequest
 *  - webhook `params.secret` and `internal` — system-managed credential and the
 *    integration binding to the source workflow's ID. The backend re-derives
 *    both on create (see enforceWebhookSecrets / normalizeWebhookTriggers).
 */
export const sanitizeWorkflowDefinitionForExport = (definition: any): any => {
  if (!definition) return definition;
  const cloned = JSON.parse(JSON.stringify(definition));

  const cleanTasks = (tasks: any[]) => {
    if (!Array.isArray(tasks)) return;
    for (const task of tasks) {
      delete task.outputs;
      // Recursively clean nested tasks (e.g. core.foreach)
      if (Array.isArray(task.params?.tasks)) {
        cleanTasks(task.params.tasks);
      }
    }
  };
  if (cloned.tasks) {
    cleanTasks(cloned.tasks);
  }

  if (Array.isArray(cloned.triggers)) {
    for (const trigger of cloned.triggers) {
      if (trigger?.type === 'webhook') {
        if (trigger.params) {
          delete trigger.params.secret;
        }
        delete trigger.internal;
      }
    }
  }

  return cloned;
};

/**
 * Serialise a workflow into the same shape the builder's #json tab renders
 * (see hooks/useJsonEditorSync.ts) — same four keys, same order, 2-space
 * indent — so the copied text can be pasted into that editor and applied.
 * `description` is added only when the automation has one: the #json tab and
 * Create from Code both read it, and without it a copy loses the description.
 */
export const buildWorkflowExportJson = (fullWorkflow: any): string =>
  JSON.stringify(
    {
      name: fullWorkflow?.name,
      ...(fullWorkflow?.description ? { description: fullWorkflow.description } : {}),
      definition: sanitizeWorkflowDefinitionForExport(fullWorkflow?.definition),
      tags: fullWorkflow?.tags ? JSON.parse(JSON.stringify(fullWorkflow.tags)) : {},
      status: fullWorkflow?.status,
    },
    null,
    2
  );

/**
 * The other direction: the workflow part of a create request, from a definition
 * pasted into Create from Code. Reads every key buildWorkflowExportJson emits,
 * so a copied automation pastes back whole. `tags` may be the exported map or a
 * list of "key:value" strings, the form the builder's settings use. The caller
 * has already checked `name` and `definition`.
 */
export const buildWorkflowRequestFromCode = (parsed: any): WorkflowRequest => {
  let tags: Record<string, any> = {};
  if (Array.isArray(parsed.tags)) {
    tags = workflowTagsToMap(parsed.tags.filter((tag: unknown) => typeof tag === 'string' && tag.trim() !== ''));
  } else if (parsed.tags && typeof parsed.tags === 'object') {
    tags = parsed.tags;
  }
  return {
    name: parsed.name,
    definition: parsed.definition,
    tags,
    status: typeof parsed.status === 'string' ? parsed.status : undefined,
    description: typeof parsed.description === 'string' ? parsed.description : undefined,
  };
};

/**
 * Absolute URL of a single automation. `accountId` is load-bearing: the builder
 * needs it to fetch, and the listing seeds its account filter from it.
 */
export const buildWorkflowShareUrl = (origin: string, workflowId: string, accountId: string): string =>
  `${origin}/automation/${workflowId}?accountId=${accountId}`;
