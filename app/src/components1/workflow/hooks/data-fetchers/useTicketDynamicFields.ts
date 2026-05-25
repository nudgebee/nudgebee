import { useState, useEffect, useRef } from 'react';
import apiTickets from '@api1/tickets';

function parseFieldValues(res: any): { label: string; value: string }[] {
  const fieldValues = res?.data?.tickets_get_field_values?.data || [];
  return fieldValues.map((m: any) => ({
    label: m.name || m.value || String(m.id),
    value: m.id || m.value,
  }));
}

interface UseTicketDynamicFieldsProps {
  isTicketCreateTask: boolean;
  // Any ticket.* task that needs the tool type resolved from the selected
  // integration (so VisibleWhen-driven fields can show/hide themselves).
  // Create tasks always need this; update/assign/etc. opt in explicitly.
  isTicketTask?: boolean;
  integrationId: string;
  projectKey: string;
  ticketType: string;
}

interface TicketFieldMeta {
  key: string;
  name: string;
  type: string;
  required?: boolean;
  allowedValues?: any[];
  autoCompleteUrl?: string;
}

interface UseTicketDynamicFieldsReturn {
  ticketProjects: { label: string; value: string }[];
  ticketProjectsLoading: boolean;
  ticketIssueTypes: { label: string; value: string }[];
  ticketIssueTypesLoading: boolean;
  ticketDynamicFields: Record<string, TicketFieldMeta>;
  ticketFieldOptions: Record<string, { label: string; value: string }[]>;
  ticketFieldOptionsLoading: Record<string, boolean>;
  ticketTool: string;
  searchTicketField: (fieldKey: string, query: string) => void;
}

export const useTicketDynamicFields = ({
  isTicketCreateTask,
  isTicketTask = false,
  integrationId,
  projectKey,
  ticketType,
}: UseTicketDynamicFieldsProps): UseTicketDynamicFieldsReturn => {
  // Configs (and the derived tool type) are needed by any ticket.* task that
  // uses VisibleWhen to branch on tool, not just create. Create tasks also
  // use them for the projects / issue-types cascade.
  const needsConfigs = isTicketCreateTask || isTicketTask;
  // Raw config data from API
  const [configsRaw, setConfigsRaw] = useState<any[]>([]);

  // Cascading state
  const [ticketProjects, setTicketProjects] = useState<{ label: string; value: string }[]>([]);
  const [ticketProjectsLoading, setTicketProjectsLoading] = useState(false);
  const [ticketIssueTypes, setTicketIssueTypes] = useState<{ label: string; value: string }[]>([]);
  const [ticketIssueTypesLoading, setTicketIssueTypesLoading] = useState(false);
  const [ticketDynamicFields, setTicketDynamicFields] = useState<Record<string, TicketFieldMeta>>({});
  const [ticketFieldOptions, setTicketFieldOptions] = useState<Record<string, { label: string; value: string }[]>>({});
  const [ticketFieldOptionsLoading, setTicketFieldOptionsLoading] = useState<Record<string, boolean>>({});
  const [ticketTool, setTicketTool] = useState('');

  // Full ticket metadata for issue-type lookup. Kept in state (not a ref) so
  // the dynamic-fields effect below re-runs after the async fetch resolves;
  // otherwise reopening a saved task — where integrationId/projectKey/ticketType
  // are all set on mount and never change — would never re-resolve the fields.
  const [ticketMetadata, setTicketMetadata] = useState<any[]>([]);

  // Per-field autoCompleteUrl cache so async searches don't need the metadata
  // lookup on every keystroke.
  const fieldUrlsRef = useRef<Record<string, string>>({});
  const searchDebounceRef = useRef<Record<string, ReturnType<typeof setTimeout>>>({});

  const fetchFieldOptions = (intId: string, fieldKey: string, autoCompleteUrl: string, query = '') => {
    setTicketFieldOptionsLoading((prev) => ({ ...prev, [fieldKey]: true }));
    apiTickets
      .getTicketFieldValues(intId, fieldKey, autoCompleteUrl, query)
      .then((res: any) => {
        setTicketFieldOptions((prev) => ({ ...prev, [fieldKey]: parseFieldValues(res) }));
      })
      .catch(() => {
        setTicketFieldOptions((prev) => ({ ...prev, [fieldKey]: [] }));
      })
      .finally(() => {
        setTicketFieldOptionsLoading((prev) => ({ ...prev, [fieldKey]: false }));
      });
  };

  const searchTicketField = (fieldKey: string, query: string) => {
    const autoCompleteUrl = fieldUrlsRef.current[fieldKey];
    if (!integrationId || !autoCompleteUrl) return;
    // Debounce per-field so rapid typing doesn't flood the backend.
    const pending = searchDebounceRef.current[fieldKey];
    if (pending) clearTimeout(pending);
    searchDebounceRef.current[fieldKey] = setTimeout(() => {
      fetchFieldOptions(integrationId, fieldKey, autoCompleteUrl, query);
    }, 200);
  };

  useEffect(
    () => () => {
      Object.values(searchDebounceRef.current).forEach((t) => clearTimeout(t));
    },
    []
  );

  // Fetch configs on mount
  useEffect(() => {
    if (!needsConfigs) return;
    setTicketProjectsLoading(true);
    apiTickets
      .listTicketConfigurations()
      .then((response: any) => {
        const configs = response?.data || [];
        setConfigsRaw(configs);
      })
      .catch(() => {
        setConfigsRaw([]);
      })
      .finally(() => {
        setTicketProjectsLoading(false);
      });
  }, [needsConfigs]);

  // When integrationId changes: resolve projects and tool type
  useEffect(() => {
    if (!needsConfigs || !integrationId || configsRaw.length === 0) {
      setTicketProjects([]);
      setTicketTool('');
      return;
    }

    const config = configsRaw.find((c: any) => c.id === integrationId);
    if (!config) {
      setTicketProjects([]);
      setTicketTool('');
      return;
    }

    setTicketTool(config.tool || '');

    // Only ticket.create uses the projects/issue-types cascade; for other
    // ticket.* tasks (update, assign, etc.) the `ticket_projects`
    // options_source in useOptionsSource handles project dropdowns directly.
    if (!isTicketCreateTask) {
      return;
    }

    // ServiceNow uses fixed project
    if (config.tool === 'servicenow') {
      setTicketProjects([{ label: 'incident', value: 'incident' }]);
      return;
    }

    const projects = config.projects || [];
    // For GitHub/GitLab, `key` is the `owner/repo` path required by the backend
    // (see ticket-server github_service.go). `name` is just the repo, which is
    // ambiguous when picked from the dropdown — show the full path instead.
    const useKeyAsLabel = config.tool === 'github' || config.tool === 'gitlab';
    setTicketProjects(
      projects.map((p: any) => ({
        label: useKeyAsLabel ? p.key : p.name || p.key,
        value: p.key,
      }))
    );
  }, [isTicketCreateTask, integrationId, configsRaw]);

  // Per-tool fixed issue-type labels for platforms that only ship one template.
  // Jira is the exception — its issue types come from the metadata response.
  const fixedIssueTypeForTool = (tool: string): { label: string; value: string }[] => {
    if (tool === 'github') return [{ label: 'Issue', value: 'Issue' }];
    if (tool === 'gitlab') return [{ label: 'Issue', value: 'issue' }];
    if (tool === 'servicenow' || tool === 'pagerduty' || tool === 'zenduty') {
      return [{ label: 'Incident', value: 'incident' }];
    }
    return [];
  };

  // When projectKey changes: fetch metadata (and derive issue types for Jira).
  useEffect(() => {
    if (!isTicketCreateTask || !integrationId || !projectKey) {
      setTicketIssueTypes([]);
      setTicketMetadata([]);
      return;
    }

    // ServiceNow has no create-meta endpoint on the backend. Use fixed labels and
    // skip the network call so we don't surface a 400.
    if (ticketTool === 'servicenow' || !ticketTool) {
      setTicketIssueTypes(fixedIssueTypeForTool(ticketTool));
      setTicketMetadata([]);
      return;
    }

    // All other supported tools (jira, github, gitlab, pagerduty, zenduty) return
    // {data: [Template, ...]} from /tickets/create-meta. Jira ships one template
    // per issue type; the other tools ship a single template carrying the
    // platform's assignee / service / urgency / labels lists. The name-matching
    // fallback in the field-resolution effect below handles single-template tools
    // whose template name (e.g. "PagerDuty Incident") doesn't match the
    // frontend's ticket_type value (e.g. "incident").
    setTicketIssueTypesLoading(true);
    apiTickets
      .getTicketMeta(integrationId, projectKey)
      .then((res: any) => {
        const metadata = res?.data?.tickets_get_create_meta?.data || [];
        setTicketMetadata(metadata);
        if (ticketTool === 'jira') {
          setTicketIssueTypes(metadata.length > 0 ? metadata.map((m: any) => ({ label: m.name, value: m.name })) : []);
        } else {
          setTicketIssueTypes(fixedIssueTypeForTool(ticketTool));
        }
      })
      .catch(() => {
        setTicketMetadata([]);
        setTicketIssueTypes(ticketTool === 'jira' ? [] : fixedIssueTypeForTool(ticketTool));
      })
      .finally(() => {
        setTicketIssueTypesLoading(false);
      });
  }, [isTicketCreateTask, integrationId, projectKey, ticketTool]);

  // When ticketType (or freshly fetched metadata) changes: resolve dynamic
  // fields. ticketMetadata is in the dep list so reopening a saved task
  // picks up fields once the async metadata fetch completes.
  useEffect(() => {
    if (!isTicketCreateTask || !ticketType || ticketMetadata.length === 0) {
      setTicketDynamicFields({});
      setTicketFieldOptions({});
      setTicketFieldOptionsLoading({});
      return;
    }

    // Case-insensitive match so providers that ship lowercase issue-type names
    // (e.g. GitHub's "bug") still align with frontend ticket_type values.
    // Single-template platforms (GitLab/PagerDuty/ZenDuty/GitHub) ship a
    // descriptive template name like "PagerDuty Incident" that doesn't match
    // the fixed ticket_type values we hand to the schema ("incident", "issue").
    // Fall back to the only template in that case so their assignee/service/
    // urgency lists actually load into the UI.
    const ticketTypeLower = ticketType.toLowerCase();
    const issueTypeMeta =
      ticketMetadata.find((m: any) => (m?.name || '').toLowerCase() === ticketTypeLower) ||
      (ticketMetadata.length === 1 ? ticketMetadata[0] : undefined);
    if (!issueTypeMeta?.fields) {
      setTicketDynamicFields({});
      setTicketFieldOptions({});
      setTicketFieldOptionsLoading({});
      return;
    }

    const fields: Record<string, TicketFieldMeta> = {};
    const options: Record<string, { label: string; value: string }[]> = {};
    const loading: Record<string, boolean> = {};
    const urls: Record<string, string> = {};

    Object.entries(issueTypeMeta.fields).forEach(([key, field]: [string, any]) => {
      // Skip summary and description — those are handled by title/description fields
      if (key === 'summary' || key === 'description') return;

      const fieldType = field.type || 'string';
      fields[key] = {
        key: field.key || key,
        name: field.name || key,
        type: fieldType,
        required: field.required || false,
        allowedValues: field.allowedValues,
        autoCompleteUrl: field.autoCompleteUrl,
      };

      if (field.autoCompleteUrl) {
        urls[key] = field.autoCompleteUrl;
      }

      // Load options from allowedValues (local)
      if (field.allowedValues && field.allowedValues.length > 0) {
        options[key] = field.allowedValues.map((v: any) => ({
          label: v.name || v.value || String(v.id),
          value: v.key || v.id || v.value,
        }));
      } else if (field.autoCompleteUrl) {
        // Seed initial options with an empty query. For user-picker fields
        // (assignee), Jira's /user/assignable/search returns the first page
        // of assignable users when the search term is empty, so the dropdown
        // isn't blank on open. Keystrokes still refine via searchTicketField
        // (debounced) for the async-user case.
        loading[key] = true;
        fetchFieldOptions(integrationId, field.key || key, field.autoCompleteUrl);
      }
    });

    // The runbook-server schema's `severity` field is keyed to `priority`
    // (Jira terminology). PagerDuty and ZenDuty surface the same concept under
    // `urgency` instead, so without aliasing the Severity dropdown stays empty
    // for those tools. Alias only when no real `priority` field exists, so we
    // never clobber a Jira project that happens to also expose `urgency`.
    if (!options.priority && options.urgency) {
      options.priority = options.urgency;
    }

    fieldUrlsRef.current = urls;
    setTicketDynamicFields(fields);
    setTicketFieldOptions((prev) => ({ ...prev, ...options }));
    setTicketFieldOptionsLoading((prev) => ({ ...prev, ...loading }));
  }, [isTicketCreateTask, integrationId, ticketType, ticketMetadata]);

  return {
    ticketProjects,
    ticketProjectsLoading,
    ticketIssueTypes,
    ticketIssueTypesLoading,
    ticketDynamicFields,
    ticketFieldOptions,
    ticketFieldOptionsLoading,
    ticketTool,
    searchTicketField,
  };
};
