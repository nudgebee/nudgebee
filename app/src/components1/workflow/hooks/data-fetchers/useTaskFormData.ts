import { useState, useEffect, useCallback, useRef } from 'react';
import apiDashboard from '@api1/home';
import apiIntegrations from '@api1/integrations';
import apiKubernetes from '@api1/kubernetes';
import apiNotifications from '@api1/notification';
import apiTickets from '@api1/tickets';
import apiResources from '@api1/resources';
import {
  JiraIcon,
  ServiceNowIcon,
  PagerDutyIcon,
  GithubIcon,
  GitLabIcon,
  ZenDutyIcon,
  PostgresIcon,
  MySqlIcon,
  ClickhouseIcon,
  ouMssql,
  ouOracle,
  ouSnowflake,
  RedisLogoIcon,
} from '@assets';

type LabelValue = { label: string; value: string };

function toNamespaceOptions(data: any[]): LabelValue[] {
  if (!data || data.length === 0) return [];
  return data.map((ns: any) => ({ label: ns.namespace_name, value: ns.namespace_name }));
}

function toWorkloadOptions(response: any): LabelValue[] {
  const workloads = response?.data || [];
  return workloads.map((w: any) => ({ label: w.name, value: w.name }));
}

function deduplicateOptions(options: LabelValue[]): LabelValue[] {
  return Array.from(new Map(options.map((o) => [o.value, o])).values());
}

// Icon mapping for ticket tools
const TICKET_TOOL_ICONS: Record<string, any> = {
  jira: JiraIcon,
  servicenow: ServiceNowIcon,
  pagerduty: PagerDutyIcon,
  github: GithubIcon,
  gitlab: GitLabIcon,
  zenduty: ZenDutyIcon,
};

const INTEGRATION_TYPE_ICONS: Record<string, any> = {
  postgresql: PostgresIcon,
  mysql: MySqlIcon,
  mssql: ouMssql,
  oracle: ouOracle,
  clickhouse: ClickhouseIcon,
  redis: RedisLogoIcon,
  snowflake: ouSnowflake,
};

const EXECUTOR_TYPE_MAP: Record<string, string> = {
  aws_ssm: 'aws',
  ssh: 'ssh',
};

const DBMS_MULTI_TYPE_TASKS = new Set(['dbms.query', 'dbms.rbms']);

// Prefixes where the second segment is used when there are 3+ parts
const MULTI_SEGMENT_PREFIXES = new Set(['dbms', 'mq']);

/**
 * Get integration type from task type string or schema SubType
 * Priority: schema SubType > task type inference
 */
const getIntegrationTypeFromSchema = (
  currentTaskDefinition: any,
  selectedActionType: string | null,
  formValues?: Record<string, any>
): string | string[] => {
  const schemaType = getIntegrationTypeFromInputSchema(currentTaskDefinition);
  if (schemaType) return schemaType;

  const executorType = getIntegrationTypeFromExecutor(selectedActionType, formValues);
  if (executorType) return executorType;

  // Cascade filter: when the user has picked a Dbms Type on a multi-type DBMS
  // task, narrow the integration list to just that type. Without this the
  // Integration Id dropdown shows every DBMS integration regardless of the
  // selected DB flavor.
  if (formValues?.dbms_type && DBMS_MULTI_TYPE_TASKS.has(selectedActionType ?? '')) {
    return formValues.dbms_type;
  }

  return inferIntegrationTypeFromAction(selectedActionType);
};

function getIntegrationTypeFromInputSchema(taskDef: any): string | null {
  if (!taskDef?.input_schema) return null;
  for (const field of Object.values<any>(taskDef.input_schema)) {
    if (field.type === 'integration' && field.sub_type) {
      return field.sub_type;
    }
  }
  return null;
}

function getIntegrationTypeFromExecutor(actionType: string | null, formValues?: Record<string, any>): string | null {
  if (actionType !== 'scripting.run_script' || !formValues?.executor_type) return null;
  return EXECUTOR_TYPE_MAP[formValues.executor_type.toLowerCase()] || null;
}

function inferIntegrationTypeFromAction(actionType: string | null): string | string[] {
  if (!actionType) return '';
  if (DBMS_MULTI_TYPE_TASKS.has(actionType)) return ['postgresql', 'mysql', 'clickhouse', 'mssql', 'oracle'];

  const parts = actionType.split('.');
  if (parts.length <= 1) return '';

  // "dbms.redis.cli" -> "redis", "mq.rabbitmqadmin.cli" -> "rabbitmqadmin"
  if (MULTI_SEGMENT_PREFIXES.has(parts[0]) && parts.length > 2) return parts[1];
  // "integrations.ssh" -> "ssh"
  if (parts[0] === 'integrations') return parts[1];

  return parts[1];
}

/**
 * Get account filter criteria from task type
 */
const getAccountCriteriaFromTask = (selectedActionType: string | null): { account_type?: string; cloud_provider?: string } | null => {
  if (!selectedActionType) return null;

  const parts = selectedActionType.split('.');
  const prefix = parts[0].toLowerCase();

  // Kubernetes tasks
  if (prefix === 'kubernetes' || prefix === 'k8s' || (prefix === 'cloud' && (parts[1] === 'k8s' || parts[1] === 'kubernetes'))) {
    return { account_type: 'kubernetes' };
  }

  // Cloud provider specific tasks
  if (prefix === 'aws' || (prefix === 'cloud' && parts[1] === 'aws')) {
    return { cloud_provider: 'AWS' };
  }
  if (prefix === 'gcp' || (prefix === 'cloud' && parts[1] === 'gcp')) {
    return { cloud_provider: 'GCP' };
  }
  if (prefix === 'azure' || (prefix === 'cloud' && parts[1] === 'azure')) {
    return { cloud_provider: 'Azure' };
  }

  return null;
};

// Custom hook for handling API data
export const useTaskFormData = (currentTaskDefinition: any, selectedActionType: string | null, formValues?: Record<string, any>) => {
  const [cloudAccounts, setCloudAccounts] = useState<{ label: string; value: string; cloud_provider?: string; account_type?: string }[]>([]);
  const [integrations, setIntegrations] = useState<{ label: string; value: string; icon?: any; type?: string }[]>([]);
  const [namespaces, setNamespaces] = useState<{ label: string; value: string }[]>([]);
  const [namespacesLoading, setNamespacesLoading] = useState(false);
  const [notifications, setNotifications] = useState<{ label: string; value: string }[]>([]);
  const [ticketConfigurations, setTicketConfigurations] = useState<
    { label: string; value: string; tool?: string; projects?: { name?: string; key?: string }[]; icon?: any }[]
  >([]);
  const [resourceTypes, setResourceTypes] = useState<{ label: string; value: string }[]>([]);
  const [resourceNames, setResourceNames] = useState<{ label: string; value: string }[]>([]);
  const [resourceNamesLoading, setResourceNamesLoading] = useState(false);
  const resourceNamesFetchTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Generation counter so a slow in-flight request doesn't overwrite results
  // from a newer one (e.g. user switches account before the previous fetch
  // resolves). Each call bumps the counter; resolvers compare before setting.
  const resourceNamesRequestId = useRef(0);
  const [workloadKinds, setWorkloadKinds] = useState<{ label: string; value: string }[]>([]);
  const [workloadKindsLoading, setWorkloadKindsLoading] = useState(false);

  // Check if any field (including nested schema fields) has a specific type
  const hasFieldType = (schema: Record<string, any> | undefined, type: string): boolean => {
    if (!schema) return false;
    return Object.values(schema).some(
      (field: any) => field.type === type || (field.type === 'object' && hasFieldType(field.schema?.properties || field.schema, type))
    );
  };

  const hasAccountField = hasFieldType(currentTaskDefinition?.input_schema, 'account');

  const hasIntegrationField =
    currentTaskDefinition?.input_schema && Object.values(currentTaskDefinition.input_schema).some((field: any) => field.type === 'integration');

  const hasNotificationField =
    currentTaskDefinition?.input_schema && Object.values(currentTaskDefinition.input_schema).some((field: any) => field.type === 'notification');

  // hasTicketField recurses so that ticket-typed fields nested inside an
  // object (e.g. gitops_config.integration_id on PV Rightsize) still trigger
  // the ticket-configurations fetch.
  const hasTicketField = hasFieldType(currentTaskDefinition?.input_schema, 'ticket');

  // Check if any field is named 'namespace'
  const hasNamespaceField =
    currentTaskDefinition?.input_schema &&
    Object.keys(currentTaskDefinition.input_schema).some((fieldName: string) => fieldName.toLowerCase() === 'namespace');

  // Check if any field is named 'kind'
  const hasKindField =
    currentTaskDefinition?.input_schema &&
    Object.keys(currentTaskDefinition.input_schema).some((fieldName: string) => fieldName.toLowerCase() === 'kind');

  // Check if there is a 'name' field alongside account/namespace/kind (resource name pattern)
  const hasResourceNameField =
    currentTaskDefinition?.input_schema &&
    Object.keys(currentTaskDefinition.input_schema).some((fieldName: string) => fieldName.toLowerCase() === 'name') &&
    (hasAccountField || hasNamespaceField || hasKindField);

  // Match node-scoped k8s tasks by action-type prefix so the Name dropdown
  // lists cluster nodes instead of workloads. Use a prefix match rather than
  // "no namespace/kind in schema" to avoid catching unrelated cluster-scoped
  // resources (Namespace, ClusterRole, StorageClass, ...) that may also have
  // a `name`-only schema in the future.
  const isK8sNodeNameField = !!hasResourceNameField && /^(k8s|kubernetes)\.node[_.]/i.test(selectedActionType ?? '');

  // Schema flags driving conditional fetch behavior. Hoisted so all effects
  // below can read them.
  const isKindReadOnly = !!currentTaskDefinition?.input_schema?.kind?.read_only;
  const kindDefault = (currentTaskDefinition?.input_schema?.kind?.default as string | undefined) || '';

  // Fetch cloud accounts when needed
  useEffect(() => {
    if (hasAccountField) {
      apiDashboard.getCloudAccounts().then((response) => {
        if (response && response.length > 0) {
          const criteria = getAccountCriteriaFromTask(selectedActionType);

          const accounts = response
            .filter((item: any) => {
              // Always check active status first
              if (item.status?.toLowerCase() !== 'active') return false;

              // If we have specific criteria, apply it
              if (criteria) {
                if (criteria.account_type && item.account_type?.toLowerCase() !== criteria.account_type.toLowerCase()) {
                  return false;
                }
                if (criteria.cloud_provider && item.cloud_provider?.toLowerCase() !== criteria.cloud_provider.toLowerCase()) {
                  return false;
                }
              }

              return true;
            })
            .map((item: any) => ({
              label: item.account_name,
              value: item.id,
              cloud_provider: item.cloud_provider,
              account_type: item.account_type,
            }));
          setCloudAccounts(accounts);
        }
      });
    }
  }, [hasAccountField, selectedActionType]);

  // Fetch integrations when needed (with SubType filtering)
  useEffect(() => {
    if (hasIntegrationField && selectedActionType) {
      const integrationType = getIntegrationTypeFromSchema(currentTaskDefinition, selectedActionType, formValues);
      const params = { limit: 100, offset: 0, type: integrationType, status: 'enabled' };

      apiIntegrations
        .listIntegrations(params)
        .then((response) => {
          if (response?.data?.data?.integrations_list?.rows && response.data.data.integrations_list.rows.length > 0) {
            const integrationOptions = response.data.data.integrations_list.rows.map((item: any) => ({
              label: item.name,
              value: item.id,
              type: item.type,
              icon: INTEGRATION_TYPE_ICONS[item.type?.toLowerCase()],
            }));
            setIntegrations(integrationOptions);
          } else {
            setIntegrations([]);
          }
        })
        .catch((error) => {
          setIntegrations([]);
          console.error('Failed to load integrations', error);
        });
    }
  }, [hasIntegrationField, selectedActionType, currentTaskDefinition, formValues?.executor_type, formValues?.dbms_type]);

  // Detect "PVC-only" tasks (e.g. k8s.pv_rightsize): kind defaults to a PVC.
  // In this mode we drive both the namespace and name dropdowns from a single
  // relay get_resource call (mirrors what KubernetesPVC.jsx does), avoiding
  // the k8s_namespaces_v2 fetch entirely and the per-namespace re-fetch in
  // the cascading resource-name effect. We don't gate on read_only because
  // older backends may not return the flag yet — kindDefault is the durable
  // signal that the task targets PVCs.
  const kindDefaultLower = kindDefault.toLowerCase();
  const isPVCMode = kindDefaultLower === 'persistentvolumeclaim' || kindDefaultLower === 'pvc' || selectedActionType === 'k8s.pv_rightsize';

  // Cache of PVCs from the most recent relay call, keyed by the account it
  // was fetched for. Stored in a ref so derive-effects don't trigger another
  // relay call.
  const pvcCacheRef = useRef<{ accountId: string; items: { name: string; namespace: string }[] }>({ accountId: '', items: [] });
  const [pvcCacheVersion, setPvcCacheVersion] = useState(0);

  // Fetch namespaces when needed, filtered by the currently-selected account
  // when the task has an account field. Falls back to all accounts otherwise.
  // Skipped in PVC mode — namespaces are derived from the relay PVC list.
  const selectedAccountId = formValues?.account || formValues?.account_id || formValues?.cloud_account_id || '';
  useEffect(() => {
    if (!hasNamespaceField || isPVCMode) return;

    // If task has an account field but nothing picked yet, clear the list so
    // users don't see namespaces from unrelated clusters.
    if (hasAccountField && !selectedAccountId) {
      setNamespaces([]);
      setNamespacesLoading(false);
      return;
    }

    let isCancelled = false;
    setNamespacesLoading(true);
    const fetchPromise = selectedAccountId
      ? apiKubernetes.getK8sNamespacesList(selectedAccountId)
      : apiDashboard.getCloudAccounts().then((accounts) => {
          if (accounts && accounts.length > 0) {
            const accountIds = accounts.map((acc: any) => acc.id);
            return apiKubernetes.getK8sNamespacesList(accountIds);
          }
          return [];
        });

    fetchPromise
      .then((namespaceData) => {
        if (isCancelled) return;
        setNamespaces(deduplicateOptions(toNamespaceOptions(namespaceData)));
      })
      .catch((error) => {
        if (isCancelled) return;
        setNamespaces([]);
        console.error('Failed to load namespaces', error);
      })
      .finally(() => {
        if (isCancelled) return;
        setNamespacesLoading(false);
      });

    return () => {
      isCancelled = true;
    };
  }, [hasNamespaceField, hasAccountField, isPVCMode, selectedAccountId]);

  // PVC mode: one relay get_resource call per account, cached. The response
  // feeds both the namespace dropdown (unique namespaces) and the name
  // dropdown (filtered client-side by the selected namespace).
  useEffect(() => {
    if (!isPVCMode) return;
    if (!selectedAccountId) {
      pvcCacheRef.current = { accountId: '', items: [] };
      setNamespaces([]);
      setResourceNames([]);
      setNamespacesLoading(false);
      setResourceNamesLoading(false);
      setPvcCacheVersion((v) => v + 1);
      return;
    }
    if (pvcCacheRef.current.accountId === selectedAccountId) return;

    let isCancelled = false;
    setNamespacesLoading(true);
    setResourceNamesLoading(true);
    apiKubernetes
      .relayForwardRequest({
        no_sinks: true,
        cache: false,
        body: {
          account_id: selectedAccountId,
          action_name: 'get_resource',
          action_params: {
            group: '',
            version: 'v1',
            resource_type: 'persistentvolumeclaims',
            all_namespaces: true,
          },
        },
      })
      .then((res: any) => {
        if (isCancelled) return;
        // Mirror KubernetesPVC.jsx parsing: findings[0].evidence[0].data is a
        // JSON string whose parsed value is [{data: <stringified items>}].
        let data = res?.data?.findings?.[0]?.evidence?.[0]?.data;
        if (typeof data === 'string') {
          try {
            const parsed = JSON.parse(data);
            data = Array.isArray(parsed) ? parsed[0]?.data : parsed;
          } catch {
            data = [];
          }
        }
        if (typeof data === 'string') {
          try {
            data = JSON.parse(data);
          } catch {
            data = [];
          }
        }
        const items: any[] = Array.isArray(data) ? data : [];
        const normalized = items
          .map((i: any) => ({ name: i?.metadata?.name as string, namespace: i?.metadata?.namespace as string }))
          .filter((i) => !!i.name && !!i.namespace);
        pvcCacheRef.current = { accountId: selectedAccountId, items: normalized };
        setPvcCacheVersion((v) => v + 1);
      })
      .catch((error: any) => {
        if (isCancelled) return;
        pvcCacheRef.current = { accountId: selectedAccountId, items: [] };
        setPvcCacheVersion((v) => v + 1);
        console.error('Failed to load PVCs', error);
      })
      .finally(() => {
        if (isCancelled) return;
        setNamespacesLoading(false);
        setResourceNamesLoading(false);
      });

    return () => {
      isCancelled = true;
    };
  }, [isPVCMode, selectedAccountId]);

  // PVC mode: derive namespace and name dropdowns from the cache. Re-runs
  // when the user picks a different namespace so the name list filters
  // client-side instead of triggering another relay call.
  const selectedNamespace = formValues?.namespace || '';
  useEffect(() => {
    if (!isPVCMode) return;
    const items = pvcCacheRef.current.items;
    setNamespaces(deduplicateOptions(items.map((i) => ({ label: i.namespace, value: i.namespace }))));
    const filtered = selectedNamespace ? items.filter((i) => i.namespace === selectedNamespace) : items;
    setResourceNames(deduplicateOptions(filtered.map((i) => ({ label: i.name, value: i.name }))));
  }, [isPVCMode, pvcCacheVersion, selectedNamespace]);

  // Fetch notification channels when needed
  useEffect(() => {
    if (hasNotificationField) {
      apiNotifications
        .getInstalledTools()
        .then((response) => {
          if (response?.messaging_platforms && response.messaging_platforms.length > 0) {
            const notificationOptions = response.messaging_platforms.map((item: any) => ({
              label: `${item.platform} (${item.id})`,
              value: item.id,
            }));
            setNotifications(notificationOptions);
          } else {
            setNotifications([]);
          }
        })
        .catch((error) => {
          setNotifications([]);
          console.error('Failed to load notification channels', error);
        });
    }
  }, [hasNotificationField]);

  // Fetch ticket configurations when needed. Carries `tool` and `projects`
  // through so nested fields can filter by tool (e.g. github only) and
  // render a project/repo dropdown sourced from integration_config_values
  // without re-fetching.
  useEffect(() => {
    if (hasTicketField) {
      apiTickets
        .listTicketConfigurations()
        .then((response: any) => {
          if (response?.data && response.data.length > 0) {
            const ticketOptions = response.data.map((item: any) => ({
              label: item.name,
              value: item.id,
              tool: item.tool,
              projects: item.projects,
              icon: TICKET_TOOL_ICONS[item.tool?.toLowerCase()],
            }));
            setTicketConfigurations(ticketOptions);
          } else {
            setTicketConfigurations([]);
          }
        })
        .catch((error) => {
          setTicketConfigurations([]);
          console.error('Failed to load ticket configurations', error);
        });
    }
  }, [hasTicketField]);

  // Fetch resource types when 'kind' field is present. Skipped when kind is
  // read_only — the dropdown is locked to the schema default, so populating
  // its option list serves no purpose.
  useEffect(() => {
    if (!hasKindField || isKindReadOnly) return;
    apiResources
      .getResourceType()
      .then((response: any) => {
        if (response?.data && Array.isArray(response.data) && response.data.length > 0) {
          // Filter out empty strings and map to label/value format
          const resourceTypeOptions = response.data
            .filter((type: string) => type && type.trim() !== '')
            .map((type: string) => ({
              label: type,
              value: type,
            }));
          setResourceTypes(resourceTypeOptions);
        } else {
          setResourceTypes([]);
        }
      })
      .catch((error) => {
        setResourceTypes([]);
        console.error('Failed to load resource types', error);
      });
  }, [hasKindField, isKindReadOnly]);

  // Fetch distinct k8s workload kinds for the selected account. Overrides the
  // static schema enum so users only see kinds actually present in their cluster.
  // Skipped when the schema marks `kind` as read_only (e.g. PV Rightsize fixes
  // it to PersistentVolumeClaim) — the dropdown is locked, so the list is unused.
  useEffect(() => {
    if (!hasKindField || !hasAccountField || isKindReadOnly) return;
    if (!selectedAccountId) {
      setWorkloadKinds([]);
      setWorkloadKindsLoading(false);
      return;
    }

    let isCancelled = false;
    setWorkloadKindsLoading(true);
    apiKubernetes
      .listK8sWorkloadWorkloadType({ accountId: selectedAccountId })
      .then((response: any) => {
        if (isCancelled) return;
        const rows = response?.data?.k8s_workloads || [];
        const kinds = rows
          .map((r: any) => r.workload_type)
          .filter((k: string) => k && k.trim() !== '')
          .map((kind: string) => ({ label: kind, value: kind }));
        setWorkloadKinds(deduplicateOptions(kinds));
      })
      .catch((error: any) => {
        if (isCancelled) return;
        setWorkloadKinds([]);
        console.error('Failed to load workload kinds', error);
      })
      .finally(() => {
        if (isCancelled) return;
        setWorkloadKindsLoading(false);
      });

    return () => {
      isCancelled = true;
    };
  }, [hasKindField, hasAccountField, isKindReadOnly, selectedAccountId]);

  // Fetch resource names when account/namespace/kind change (cascading dropdown).
  // For node-scoped k8s tasks, list cluster nodes instead of workloads.
  const fetchResourceNames = useCallback((account: string, namespace: string, kind: string, isNodeName: boolean) => {
    if (resourceNamesFetchTimer.current) {
      clearTimeout(resourceNamesFetchTimer.current);
    }

    if (!account && !namespace && !kind) {
      resourceNamesRequestId.current += 1;
      setResourceNames([]);
      setResourceNamesLoading(false);
      return;
    }

    setResourceNamesLoading(true);
    const requestId = ++resourceNamesRequestId.current;
    const isLatest = () => resourceNamesRequestId.current === requestId;

    resourceNamesFetchTimer.current = setTimeout(() => {
      // Node-scoped k8s tasks (e.g. node graceful shutdown): list cluster nodes
      // instead of workloads.
      if (isNodeName) {
        if (!account) {
          if (isLatest()) {
            setResourceNames([]);
            setResourceNamesLoading(false);
          }
          return;
        }
        apiKubernetes
          .getK8sNodes({ accountId: account, isActive: true, nodeName: '', limit: 500, offset: 0 })
          .then((response: any) => {
            if (!isLatest()) return;
            const nodes = response?.data?.k8s_nodes || [];
            setResourceNames(deduplicateOptions(nodes.map((n: any) => ({ label: n.name, value: n.name }))));
          })
          .catch((error: any) => {
            if (!isLatest()) return;
            setResourceNames([]);
            console.error('Failed to load nodes', error);
          })
          .finally(() => {
            if (isLatest()) setResourceNamesLoading(false);
          });
        return;
      }

      // PVCs aren't in the workloads table — fetch via relay get_resource.
      const kindLower = (kind || '').toLowerCase();
      const isPVC = kindLower === 'persistentvolumeclaim' || kindLower === 'pvc';
      if (isPVC) {
        if (!account) {
          if (isLatest()) {
            setResourceNames([]);
            setResourceNamesLoading(false);
          }
          return;
        }
        apiKubernetes
          .relayForwardRequest({
            no_sinks: true,
            cache: false,
            body: {
              account_id: account,
              action_name: 'get_resource',
              action_params: {
                group: '',
                version: 'v1',
                resource_type: 'persistentvolumeclaims',
                all_namespaces: !namespace,
                ...(namespace ? { namespace } : {}),
              },
            },
          })
          .then((res: any) => {
            if (!isLatest()) return;
            let data = res?.data?.findings?.[0]?.evidence?.[0]?.data;
            if (typeof data === 'string') {
              try {
                const parsed = JSON.parse(data);
                data = Array.isArray(parsed) ? parsed[0]?.data : parsed;
              } catch {
                data = [];
              }
            }
            if (typeof data === 'string') {
              try {
                data = JSON.parse(data);
              } catch {
                data = [];
              }
            }
            const items: any[] = Array.isArray(data) ? data : [];
            const filtered = namespace ? items.filter((i: any) => i?.metadata?.namespace === namespace) : items;
            setResourceNames(
              deduplicateOptions(filtered.map((i: any) => ({ label: i?.metadata?.name, value: i?.metadata?.name })).filter((o: any) => !!o.value))
            );
          })
          .catch((error: any) => {
            if (!isLatest()) return;
            setResourceNames([]);
            console.error('Failed to load PVCs', error);
          })
          .finally(() => {
            if (isLatest()) setResourceNamesLoading(false);
          });
        return;
      }

      const query: Record<string, any> = {};
      if (account) query.accountId = account;
      if (namespace) query.namespace = namespace;
      if (kind) query.kind = kind;
      query.is_active = true;

      apiKubernetes
        .getAllK8sWorkload(query)
        .then((response: any) => {
          if (!isLatest()) return;
          setResourceNames(deduplicateOptions(toWorkloadOptions(response)));
        })
        .catch((error: any) => {
          if (!isLatest()) return;
          setResourceNames([]);
          console.error('Failed to load resource names', error);
        })
        .finally(() => {
          if (isLatest()) setResourceNamesLoading(false);
        });
    }, 300);
  }, []);

  // Watch form values for cascading resource name fetch. Fall back to the
  // schema's `kind.default` so tasks where Kind is read-only with a default
  // route to the right branch on first render. Skipped in PVC mode — the
  // dedicated PVC effect above already drives the name list from the cache.
  useEffect(() => {
    if (!hasResourceNameField || !formValues || isPVCMode) return;

    const account = formValues.account || formValues.account_id || formValues.cloud_account_id || '';
    const namespace = formValues.namespace || '';
    const kind = formValues.kind || kindDefault || '';

    fetchResourceNames(account, namespace, kind, isK8sNodeNameField);

    return () => {
      if (resourceNamesFetchTimer.current) {
        clearTimeout(resourceNamesFetchTimer.current);
      }
    };
  }, [
    hasResourceNameField,
    isK8sNodeNameField,
    isPVCMode,
    formValues?.account,
    formValues?.account_id,
    formValues?.cloud_account_id,
    formValues?.namespace,
    formValues?.kind,
    kindDefault,
    fetchResourceNames,
  ]);

  return {
    cloudAccounts,
    integrations,
    namespaces,
    namespacesLoading,
    notifications,
    ticketConfigurations,
    resourceTypes,
    resourceNames,
    resourceNamesLoading,
    workloadKinds,
    workloadKindsLoading,
    hasResourceNameField: !!hasResourceNameField,
  };
};
