import React, { useEffect, useState } from 'react';
import { Box, Divider, Typography } from '@mui/material';
import TenantAccountCommonSettings from './TenantAccountCommonSettings';
import CustomTextField from '@components1/common/CustomTextField';
import { Modal } from './modal';
import { deleteTenantAttributes, getFeatures, getTenantAttributes, updateTenantFeatureFlag, upsertTenantAttributes } from '@lib/UserService';
import { snackbar } from './snackbarService';
import { parseHttpResponseBodyMessage, safeJSONParse } from 'src/utils/common';
import CustomButton from './NewCustomButton';
import CustomCheckBox from './CustomCheckbox';
import { fetchFeatureFlagsForTenant } from '@lib/auth';
import { useSession } from 'next-auth/react';
import apiUserManagement from '@api1/user';
import TextWithBorder from './TextWithBorder';
import FilterDropdownButton from './FilterDropdownButton';

const DEFAULT_SUBJECT_NAME_LABELS = [
  'destination_workload_name',
  'src_workload_name',
  'deployment',
  'daemonset',
  'statefulset',
  'app_id',
  'nb_alert_job',
  'service_name',
  'service.name',
  'pod',
  'pod_name',
  'container',
  'nb_resource_id',
  'job',
];
const DEFAULT_NAMESPACE_LABELS = ['destination_workload_namespace', 'namespace', 'k8s_namespace', 'k8s.namespace.name'];
const DEFAULT_SEVERITY_LABELS = ['severity'];

const COMMON_WEBHOOK_LABEL_KEYS = [
  'alertname',
  'severity',
  'priority',
  'level',
  'namespace',
  'destination_workload_namespace',
  'k8s_namespace',
  'k8s.namespace.name',
  'service_name',
  'service.name',
  'app_id',
  'app_name',
  'deployment',
  'daemonset',
  'statefulset',
  'pod',
  'pod_name',
  'container',
  'job',
  'instance',
  'cluster',
  'team',
  'env',
  'environment',
  'executor_name',
  'saas_env',
  'destination_workload_name',
  'src_workload_name',
  'nb_alert_job',
  'nb_resource_id',
  'monitorName',
  'rulename',
  'related_logs',
  'rule_id',
  'rule_type',
];

const TenantSettings = ({ open, title, onClose }) => {
  const { data: session } = useSession();
  const VALID_ROLES = ['tenant_admin', 'tenant_admin_readonly'];

  const [logSettings, setLogSettings] = useState({
    logPodLabel: '',
    logNamespaceLabel: '',
    logAppLabel: '',
    logDefaultQuery: '',
  });
  const [loading, setLoading] = useState(false);
  const [selectedFeatures, setSelectedFeatures] = useState([]);
  const [initialFeatures, setInitialFeatures] = useState([]); // <-- track original
  const [featureOptions, setFeatureOptions] = useState([]);
  const [tenantName, setTenantName] = useState(session?.tenant?.name);
  const [checkboxEnabled, setCheckboxEnabled] = useState(false);
  const [allowDomainValue, setAllowDomainValue] = useState('');
  const [defaultAuthRole, setDefaultAuthRole] = useState('');
  const [selectedObservabilityPlatform, setSelectedObservabilityPlatform] = useState('');
  const [logClusterLabel, setLogClusterLabel] = useState('');
  const [webhookLabelMapping, setWebhookLabelMapping] = useState({
    subject_name_labels: DEFAULT_SUBJECT_NAME_LABELS,
    namespace_labels: DEFAULT_NAMESPACE_LABELS,
    severity_labels: DEFAULT_SEVERITY_LABELS,
  });
  const [webhookMappingSaved, setWebhookMappingSaved] = useState(false);
  const [webhookMappingModified, setWebhookMappingModified] = useState(false);

  useEffect(() => {
    const fetchTenantAttributes = async () => {
      try {
        setLoading(true);
        const tenantAttributes = await getTenantAttributes();
        const features = await getFeatures();
        if (features.length > 0) {
          setFeatureOptions(features);
        }

        if (tenantAttributes) {
          const logLabelValues = tenantAttributes.find((attr) => attr.name === 'log_labels');
          const allowedDomains = tenantAttributes.find((attr) => attr.name === 'allowed_domains');
          const defaultLogProvider = tenantAttributes.find((attr) => attr.name === 'default_log_provider');
          setSelectedObservabilityPlatform(defaultLogProvider?.value || '');
          const logClusterLabel = tenantAttributes.find((attr) => attr.name === 'log_cluster_label');
          setLogClusterLabel(logClusterLabel?.value || '');
          if (logLabelValues && Object.keys(logLabelValues).length > 0) {
            const labels = safeJSONParse(logLabelValues.value) ?? logLabelValues.value;
            setLogSettings({
              logPodLabel: labels.pod || '',
              logNamespaceLabel: labels.namespace || '',
              logAppLabel: labels.app || '',
              logDefaultQuery: labels.defaultQuery || '',
            });
          }
          if (allowedDomains && Object.keys(allowedDomains).length > 0) {
            try {
              const parsedDomains = safeJSONParse(allowedDomains.value) ?? allowedDomains.value;
              const validDomains = Array.isArray(parsedDomains) ? parsedDomains.filter((d) => d?.trim()) : [];
              setCheckboxEnabled(validDomains.length > 0);
              setAllowDomainValue(validDomains.join(','));
            } catch (error) {
              console.error('Failed to parse allowed domains', error);
              setCheckboxEnabled(false);
              setAllowDomainValue('');
            }
          }
          const defaultRoleAttr = tenantAttributes.find((attr) => attr.name === 'auth_default_role');
          if (defaultRoleAttr && defaultRoleAttr.value) {
            setDefaultAuthRole(defaultRoleAttr.value);
          }

          const webhookMapping = tenantAttributes.find((attr) => attr.name === 'webhook_label_mapping');
          if (webhookMapping?.value) {
            try {
              const parsed = safeJSONParse(webhookMapping.value) ?? webhookMapping.value;
              setWebhookLabelMapping({
                subject_name_labels: parsed.subject_name_labels ?? DEFAULT_SUBJECT_NAME_LABELS,
                namespace_labels: parsed.namespace_labels ?? DEFAULT_NAMESPACE_LABELS,
                severity_labels: parsed.severity_labels ?? DEFAULT_SEVERITY_LABELS,
              });
              setWebhookMappingSaved(true);
            } catch (e) {
              console.error('Failed to parse webhook_label_mapping', e);
            }
          }
        }

        const tenantFeatureFlags = await fetchFeatureFlagsForTenant();
        if (tenantFeatureFlags?.length > 0) {
          const enabled = tenantFeatureFlags.filter((g) => g.status === 'enabled').map((g) => g.feature_id);

          setSelectedFeatures(enabled);
          setInitialFeatures(enabled); // <-- save original state
        }
      } catch (error) {
        snackbar.error(`Failed to fetch Tenant settings - ${parseHttpResponseBodyMessage(error)}`);
      } finally {
        setLoading(false);
      }
    };

    const fetchTenant = async () => {
      try {
        apiUserManagement.listUserTenants(session?.user?.email).then((res) => {
          const tenants = res.data ?? [];
          if (tenants.length > 0) {
            setTenantName(tenants.filter((t) => t.name == session?.tenant?.name)?.[0]?.name || '');
          }
        });
      } catch {
        setTenantName('');
      }
    };

    if (open) {
      fetchTenantAttributes();
      fetchTenant();
    }
  }, [open]);

  const handleSaveSettings = async () => {
    setLoading(true);
    try {
      if (checkboxEnabled && !allowDomainValue.trim()) {
        snackbar.error('Allowed Domains field cannot be empty when domain login is enabled.');
        setLoading(false);
        return;
      }
      if (checkboxEnabled && defaultAuthRole.trim() !== '' && !VALID_ROLES.includes(defaultAuthRole.trim())) {
        snackbar.error("Invalid role. Allowed roles are 'tenant_Admin' or 'tenant_admin_readonly'.");
        setLoading(false);
        return;
      }
      // Save Loki settings
      const attrsToSave = [
        {
          name: 'log_labels',
          value: JSON.stringify({
            pod: logSettings.logPodLabel,
            namespace: logSettings.logNamespaceLabel,
            app: logSettings.logAppLabel,
            defaultQuery: logSettings.logDefaultQuery,
          }),
        },
        { name: 'default_log_provider', value: selectedObservabilityPlatform ? selectedObservabilityPlatform : '' },
        { name: 'log_cluster_label', value: logClusterLabel ? logClusterLabel : '' },
      ];
      if (webhookMappingModified || webhookMappingSaved) {
        attrsToSave.push({
          name: 'webhook_label_mapping',
          value: JSON.stringify({
            subject_name_labels: webhookLabelMapping.subject_name_labels,
            namespace_labels: webhookLabelMapping.namespace_labels,
            severity_labels: webhookLabelMapping.severity_labels,
          }),
        });
      }
      const response = await upsertTenantAttributes(attrsToSave);

      if (response?.data?.errors) {
        snackbar.error(`Failed to save loki labels configuration - ${parseHttpResponseBodyMessage(response.data)}`);
        return;
      }

      // Determine changed feature flags
      const added = selectedFeatures.filter((f) => !initialFeatures.includes(f));
      const removed = initialFeatures.filter((f) => !selectedFeatures.includes(f));

      const updatePayload = [
        ...added.map((f) => ({ feature_id: f, status: 'enabled' })),
        ...removed.map((f) => ({ feature_id: f, status: 'disabled' })),
      ];

      if (updatePayload.length > 0) {
        const updateFeatureFlagResponse = await updateTenantFeatureFlag(updatePayload);
        if (updateFeatureFlagResponse?.data?.errors) {
          snackbar.error(`Failed to save feature configuration - ${parseHttpResponseBodyMessage(updateFeatureFlagResponse.data)}`);
          return;
        }
        snackbar.success('Feature configuration saved.');
        fetchFeatureFlagsForTenant(true); // refresh cache
      }
      if (checkboxEnabled) {
        const updateAllowedLoginDomainResponse = await upsertTenantAttributes([
          {
            name: 'allowed_domains',
            value: JSON.stringify(
              allowDomainValue
                ? allowDomainValue
                    .split(',')
                    .map((d) => d.trim())
                    .filter(Boolean)
                : []
            ),
          },
          {
            name: 'auth_default_role',
            value: defaultAuthRole || '',
          },
        ]);
        if (updateAllowedLoginDomainResponse?.data?.errors) {
          snackbar.error(`Failed to save allowed login domain - ${parseHttpResponseBodyMessage(updateAllowedLoginDomainResponse.data)}`);
          return;
        }
      } else if (!checkboxEnabled) {
        const deleteAllowedLoginDomainResponse = await deleteTenantAttributes(['allowed_domains', 'auth_default_role']);
        if (deleteAllowedLoginDomainResponse?.data?.errors) {
          snackbar.error(`Failed to delete allowed login domain - ${parseHttpResponseBodyMessage(deleteAllowedLoginDomainResponse.data)}`);
          return;
        }
      }
      // Update Tenant Name
      if (tenantName !== session?.tenant?.name) {
        const response = await fetch('/api/tenant/update-name', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ tenantName }),
        });
        const data = await response.json();
        if (!response.ok || data?.errors) {
          snackbar.error(`Failed to update tenant name - ${parseHttpResponseBodyMessage(data)}`);
          return;
        }
        setTenantName('');
      }
    } catch (error) {
      snackbar.error(`Error while saving settings - ${parseHttpResponseBodyMessage(error)}`);
    } finally {
      await getTenantAttributes(true);
      setLoading(false);
    }

    onClose(null, 'show');
  };

  const handleCheckBoxChange = (featureValue) => {
    setSelectedFeatures(
      (prev) =>
        prev.includes(featureValue)
          ? prev.filter((val) => val !== featureValue) // Uncheck
          : [...prev, featureValue] // Check
    );
  };

  const handleClose = () => {
    onClose(null, 'hide');
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      loader={loading}
      width='md'
      sx={{
        '& .MuiPaper-root': {
          maxWidth: '1010px',
          '& .MuiDialogContent-root': {
            padding: '32px 40px 0px 40px',
          },
        },
      }}
    >
      <Box sx={{ display: 'flex', flexDirection: 'column', gap: '24px' }}>
        <CustomTextField
          label='Tenant Name'
          value={tenantName}
          fullWidth={false}
          sx={{ width: '40%' }}
          onChange={(e) => {
            setTenantName(e.target.value);
          }}
        />
        <Divider sx={{ my: '12px' }} />
        <Box display='flex' flexDirection='column' gap={1}>
          <CustomCheckBox
            checked={checkboxEnabled}
            text='For Self-Onboarding Enable specific domain login'
            onChange={() => setCheckboxEnabled((prev) => !prev)}
            checkboxStyle={{
              marginLeft: '6px',
            }}
          />
          <Box display='flex' flexDirection='column' gap={2} flex={1}>
            <CustomTextField
              label='Allowed Domains'
              value={allowDomainValue}
              fullWidth={false}
              sx={{ width: '40%' }}
              onChange={(e) => setAllowDomainValue(e.target.value)}
              disabled={!checkboxEnabled}
              placeholder='Enter allowed login domains, such as gmail.com'
            />
            <CustomTextField
              label='Default Auth Role'
              value={defaultAuthRole}
              fullWidth={false}
              sx={{ width: '40%' }}
              instructionText='Only "tenant_admin" or "tenant_admin_readonly" are allowed'
              onChange={(e) => setDefaultAuthRole(e.target.value?.trim())}
              disabled={!checkboxEnabled}
              placeholder='Enter default auth role for self-onboarding'
            />
          </Box>
        </Box>
        <Divider sx={{ my: '12px' }} />
        <TenantAccountCommonSettings logSettings={logSettings} setLogSettings={setLogSettings} />
        <Divider sx={{ my: '12px' }} />
        <CustomTextField
          label='Cluster Label'
          value={logClusterLabel}
          onChange={(e) => setLogClusterLabel(e.target.value)}
          placeholder='example: {cluster_name="k8s-cluster"}'
        />

        <Divider sx={{ my: 2 }} />
        <Box display='flex' flexDirection='column' gap={2}>
          <TextWithBorder
            value='Webhook Label Mapping'
            borderColor='#3B82F6'
            borderWidth='3px'
            sx={{
              '& p': {
                fontSize: '16px',
                fontWeight: 500,
                color: '#374151',
                lineHeight: '24px',
              },
            }}
          />
          <Typography variant='body2' sx={{ ml: '12px', color: '#6B7280', fontSize: '13px', lineHeight: '20px' }}>
            Map alert label keys to event fields. Order matters — the first label with a non-empty value is used. You can type custom label keys not
            in the suggestions. For advanced extraction, use Jinja2 templates (e.g. {"{{ labels.app_id | split(sep='/') | last }}"}) or regex (e.g.
            app_id|/k8s/[^/]+/(.+)).
          </Typography>
          <Box sx={{ ml: '12px', display: 'flex', flexDirection: 'column', gap: 2 }}>
            <FilterDropdownButton
              multiple
              freeSolo
              label='Subject Name Labels'
              value={webhookLabelMapping.subject_name_labels}
              options={[...new Set([...COMMON_WEBHOOK_LABEL_KEYS, ...webhookLabelMapping.subject_name_labels])]}
              onSelect={(e) => {
                setWebhookLabelMapping((prev) => ({ ...prev, subject_name_labels: e.target.value }));
                setWebhookMappingModified(true);
              }}
              limitTag={3}
            />
            <FilterDropdownButton
              multiple
              freeSolo
              label='Namespace Labels'
              value={webhookLabelMapping.namespace_labels}
              options={[...new Set([...COMMON_WEBHOOK_LABEL_KEYS, ...webhookLabelMapping.namespace_labels])]}
              onSelect={(e) => {
                setWebhookLabelMapping((prev) => ({ ...prev, namespace_labels: e.target.value }));
                setWebhookMappingModified(true);
              }}
              limitTag={3}
            />
            <FilterDropdownButton
              multiple
              freeSolo
              label='Severity Labels'
              value={webhookLabelMapping.severity_labels}
              options={[...new Set([...COMMON_WEBHOOK_LABEL_KEYS, ...webhookLabelMapping.severity_labels])]}
              onSelect={(e) => {
                setWebhookLabelMapping((prev) => ({ ...prev, severity_labels: e.target.value }));
                setWebhookMappingModified(true);
              }}
              limitTag={3}
            />
          </Box>
        </Box>

        <Divider sx={{ my: 2 }} />
        <Box display='flex' flexDirection='column' gap={2} mb='40px'>
          <TextWithBorder
            value='Feature Flag'
            borderColor='#3B82F6'
            borderWidth='3px'
            sx={{
              '& p': {
                fontSize: '16px',
                fontWeight: 500,
                color: '#374151',
                lineHeight: '24px',
              },
            }}
          />
          <Box
            display='grid'
            gridTemplateColumns='repeat(3, 1fr)'
            sx={{
              ml: '12px',
              width: '100%',
              '& > *': {
                borderRight: '1px solid #EBEBEB',
                borderBottom: '1px solid #EBEBEB',
                padding: '12px 16px',
                '&:nth-of-type(3n)': {
                  borderRight: 'none',
                },
                '&:nth-last-of-type(-n+3)': {
                  borderBottom: 'none',
                },
              },
            }}
          >
            {featureOptions.map((f) => (
              <CustomCheckBox
                key={f.value}
                checked={selectedFeatures.includes(f.value)}
                text={f.description || f.value}
                onChange={() => handleCheckBoxChange(f.value)}
                checkboxStyle={{ fontSize: '12px' }}
              />
            ))}
          </Box>
        </Box>
      </Box>
      <Box
        display='flex'
        alignItems='center'
        justifyContent='flex-end'
        gap='12px'
        p='16px 24px'
        sx={{
          borderTop: '0.5px solid #EBEBEB',
          '& button': { minWidth: '140px' },
          position: 'sticky',
          bottom: 0,
          backgroundColor: 'white',
          zIndex: 1,
        }}
      >
        <CustomButton
          variant='secondary'
          size='Medium'
          onClick={() => {
            handleClose();
          }}
          text={'Cancel'}
        />
        <CustomButton size='Medium' onClick={handleSaveSettings} text={'Save'} disabled={loading} />
      </Box>
    </Modal>
  );
};

export default TenantSettings;
