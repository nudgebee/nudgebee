import React, { useEffect, useMemo, useState } from 'react';
import { Box } from '@mui/material';
import { Input } from '@components1/ds/Input';
import { Form } from '@components1/ds/Form';
import TicketsDescriptionEditor from './TicketsDescriptionEditor';
import apiTickets from '@api1/tickets';
import PropTypes from 'prop-types';
import TicketFormComponent from './TicketFormComponent';
import { JiraIcon, ServiceNowIcon, GithubIcon, GitLabIcon, PagerDutyIcon, ZenDutyIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import { Select } from '@components1/ds/Select';

/**
 * Get icon for integration tool types
 * @param {string} tool - Tool type (jira, servicenow, github, pagerduty, zenduty)
 * @returns {Object} - Icon source
 */
const getToolIcon = (tool) => {
  const toolLower = tool?.toLowerCase();

  switch (toolLower) {
    case 'jira':
      return JiraIcon;
    case 'servicenow':
      return ServiceNowIcon;
    case 'github':
      return GithubIcon;
    case 'gitlab':
      return GitLabIcon;
    case 'pagerduty':
      return PagerDutyIcon;
    case 'zenduty':
      return ZenDutyIcon;
    default:
      return null;
  }
};

/**
 * TicketFormSection - Main form component for creating and editing tickets
 *
 * This component provides the primary form interface for ticket management,
 * handling the selection of config, project, and issue type, as well as
 * managing the dynamic fields that change based on these selections.
 *
 * @param {Object} ticketUrl - URL information for the ticket
 * @param {Object} ticketData - Initial ticket data for populating the form
 * @param {Boolean} error - Whether there are validation errors
 * @param {Function} onStateChange - Callback for form state changes
 * @param {Array} ignoreFields - Fields to exclude from rendering
 * @param {Boolean} isEdit - Whether in edit mode (affects data processing)
 * @param {Boolean} viewOnlyMode - Whether the form should be read-only
 */
const TicketFormSection = ({
  ticketUrl = { url: '' },
  ticketData = {
    subject: '',
    description: '',
  },
  toolName = '',
  onStateChange,
  ignoreFields = [],
  isEdit = false,
  forceValidate = false,
  viewOnlyMode = false,
}) => {
  // Ticket subject and description
  const [ticketDetails, setTicketDetails] = React.useState({
    subject: ticketData?.subject ?? '',
    description: ticketData?.description?.replace(/~/g, '&#126;') ?? '',
  });

  // Issue type dropdown options and selection
  const [issueTypes, setIssueTypes] = useState([]);
  const [selectedIssueType, setSelectedIssueType] = useState('');

  // Metadata for ticket fields based on selected issue type
  const [ticketMetadata, setTicketMetadata] = useState([]);
  const [selectedIssueTypeTicketMetadata, setSelectedIssueTypeTicketMetadata] = useState([]);

  // Ticket configuration and project selections
  const [selectedConfig, setSelectedConfig] = React.useState(ticketData?.configuration_id || ticketData?.selectedConfig?.id);
  const [selectedProject, setSelectedProject] = React.useState(ticketData?.selectedProject ?? null);

  // Lists for dropdowns
  const [configList, setConfigList] = React.useState([]);
  const [allProjectList, setAllProjectList] = useState([]);
  const [projectList, setProjectList] = React.useState([]);

  // Loading state for issue types
  const [loadingIssueTypes, setLoadingIssueTypes] = useState(false);

  const getFieldErrorMessage = (fieldName, value, customMessage = null) => {
    if (forceValidate && !value) {
      return customMessage || `${fieldName} is required`;
    }
    return '';
  };

  /**
   * Converts dates from ISO strings back to timestamps
   * Used when editing existing tickets to handle date conversions
   *
   * @param {Object} processedObj - The object with values to process
   * @returns {Object} - The object with dates converted to timestamps
   */
  const reverseProcessing = (processedObj) => {
    // Guard against null/undefined
    if (!processedObj) {
      return {};
    }

    const revertedObj = { ...processedObj };
    const fields = selectedIssueTypeTicketMetadata?.[0]?.fields;

    if (fields) {
      Object.entries(fields).forEach(([key, value]) => {
        if (value && value.type === 'datepicker' && processedObj[key]) {
          // Convert ISO date string back to original format
          revertedObj[key] = new Date(processedObj[key]).getTime();
        } else if (value && value.type === 'datetime' && processedObj[key]) {
          // Convert ISO datetime string back to original format
          revertedObj[key] = new Date(processedObj[key]).getTime();
        }
      });
    }

    if (ticketData?.assignee) {
      revertedObj.assignee = ticketData.assignee;
    }

    return revertedObj;
  };

  // Initialize formData with reversed values if in edit mode
  const [formData, setFormData] = useState(isEdit ? reverseProcessing(ticketData?.additionalFields) : {});

  const validateForm = () => {
    let isValid = true;
    const errors = {};

    // 1. Validate Static Fields
    if (!selectedConfig) {
      isValid = false;
      errors.config = 'Configuration is required';
    }
    if (!selectedProject) {
      isValid = false;
      errors.project = 'Project is required';
    }
    if (!selectedIssueType) {
      isValid = false;
      errors.issueType = 'Issue Type is required';
    }
    if (!ignoreFields.includes('subject') && !ticketDetails.subject?.trim()) {
      isValid = false;
      errors.subject = 'Subject is required';
    }

    // 2. Validate Dynamic Fields (based on Jira/System metadata)
    // We check the fields definition currently loaded
    const currentMeta = selectedIssueTypeTicketMetadata?.[0]?.fields;

    if (currentMeta) {
      Object.entries(currentMeta).forEach(([fieldKey, fieldConfig]) => {
        // Check if the field is marked as required in the metadata
        if (fieldKey == 'summary') {
          return;
        }
        if (fieldConfig.required) {
          const value = formData[fieldKey];

          // Check if value is empty/null/undefined
          // Note: Adjust logic if '0' or 'false' are valid values for your use case
          if (value === null || value === undefined || value === '' || (Array.isArray(value) && value.length === 0)) {
            isValid = false;
            errors[fieldKey] = `${fieldConfig.name} is required`;
          }
        }
      });
    }

    return { isValid, errors };
  };

  /**
   * Synchronize form state with parent component
   */
  useEffect(() => {
    // GUARD CLAUSE:
    // If in Edit mode, and we have initial data (ticketData), but config isn't selected yet,
    // it means we are still "Loading/Hydrating". DO NOT fire the update yet.
    if (isEdit && ticketData?.selectedConfig?.id && !selectedConfig) {
      return;
    }

    const { isValid, errors } = validateForm();
    const newState = {
      selectedConfig,
      selectedProject,
      selectedIssueType,
      ticketDetails,
      formData,
      selectedIssueTypeTicketMetadata,
      isValid,
      formErrors: errors,
    };

    // Only trigger update if the state actually changed
    if (onStateChange && JSON.stringify(newState) !== JSON.stringify(ticketData)) {
      onStateChange(newState);
    }
  }, [selectedConfig, selectedProject, selectedIssueType, ticketDetails, formData, selectedIssueTypeTicketMetadata]);

  /**
   * Fetches available ticket configurations and projects from the API
   */
  const fetchConfigList = () => {
    apiTickets
      .listTicketConfigurations(
        {
          tool: toolName,
        },
        true
      )
      .then((res) => {
        setConfigList(res?.data);
        if (res?.data && res?.data.length > 0) {
          // Extract project data from each configuration
          const allProjects =
            res?.data?.flatMap(
              (config) =>
                config?.projects?.map((d) => ({
                  name: d?.name,
                  key: d?.key,
                  pName: config?.name,
                })) || []
            ) || [];
          setAllProjectList(allProjects);
        }
      });
  };

  // Fetch config list on component mount
  useEffect(() => {
    fetchConfigList();
  }, []);

  /**
   * Set the selected config when ticketData changes
   * Used when populating form in edit mode
   */
  useEffect(() => {
    // Handle both nested object and flat ID
    const incomingConfigId = ticketData?.selectedConfig?.id || ticketData?.configuration_id;

    // Only update if we have an incoming ID AND it's different from current
    // This prevents re-setting state if the ID is strictly the same
    if (incomingConfigId && incomingConfigId != selectedConfig?.id) {
      const foundConfig = configList.find((c) => c.id == incomingConfigId);
      if (foundConfig) {
        setSelectedConfig(foundConfig);
      }
    }
  }, [ticketData, configList]);

  /**
   * Set the selected project when projectList changes
   * Used when populating form in edit mode or when config changes
   */
  useEffect(() => {
    const incomingProjectKey = ticketData?.projectKey || ticketData?.project_key;

    if (isEdit && incomingProjectKey && selectedProject?.key != incomingProjectKey) {
      const foundProject = projectList.find((p) => p.key == incomingProjectKey);
      if (foundProject) {
        setSelectedProject(foundProject);
      }
    }
  }, [projectList, selectedConfig]);

  /**
   * Set the selected issue type and update metadata
   * when issue types load or ticketData changes
   */
  useEffect(() => {
    const incomingType = ticketData?.ticketType || ticketData?.ticket_type;

    if (incomingType && incomingType != selectedIssueType) {
      setSelectedIssueType(incomingType);
    }

    // Filter metadata (Keep existing logic)
    if (ticketMetadata.length > 0) {
      setSelectedIssueTypeTicketMetadata(ticketMetadata.filter((n) => n.name === selectedIssueType));
    }
  }, [issueTypes]);

  /**
   * Filter project list based on selected configuration
   * For servicenow, use fixed 'incident' value
   */
  useEffect(() => {
    if (selectedConfig?.tool === 'servicenow') {
      const fixedProject = { name: 'incident', key: 'incident', pName: selectedConfig?.name };
      setProjectList([fixedProject]);
      setSelectedProject(fixedProject);
    } else {
      const projectList = allProjectList.filter((d) => d.pName === selectedConfig?.name);
      setProjectList(projectList);
      setSelectedProject(null);
    }
  }, [selectedConfig]);

  /**
   * Fetch issue types when project changes
   * Different actions based on ticket system type (Jira, GitHub, ServiceNow, etc)
   */
  useEffect(() => {
    if (selectedConfig?.tool === 'jira' && selectedProject) {
      // For Jira, fetch issue types and metadata from API
      setLoadingIssueTypes(true);
      apiTickets
        .getTicketMeta(selectedConfig.id, selectedProject.key)
        .then((res) => {
          const ticketMetadata = res?.data?.tickets_get_create_meta?.data || [];
          if (ticketMetadata && ticketMetadata.length > 0) {
            setIssueTypes(ticketMetadata.map((b) => b.name));
            setTicketMetadata(ticketMetadata);
          }
        })
        .finally(() => {
          setLoadingIssueTypes(false);
        });
    } else if (selectedConfig?.tool === 'github') {
      // For GitHub, use predefined issue types
      setIssueTypes(['bug']);
    } else if (selectedConfig?.tool === 'gitlab') {
      // For GitLab, use predefined issue types
      setIssueTypes(['issue']);
    } else if (selectedConfig?.tool === 'servicenow' || selectedConfig?.tool === 'pagerduty' || selectedConfig?.tool === 'zenduty') {
      // For ServiceNow, PagerDuty, and ZenDuty, use "incident" type
      setIssueTypes(['incident']);
    }
  }, [selectedProject]);

  /**
   * Update selected issue type metadata when issue type changes
   */
  useEffect(() => {
    if (selectedIssueType && selectedConfig?.tool === 'jira') {
      const getMetadataOfIssueType = ticketMetadata?.filter((n) => n.name === selectedIssueType);
      if (getMetadataOfIssueType && getMetadataOfIssueType.length > 0) {
        setSelectedIssueTypeTicketMetadata(getMetadataOfIssueType);
      }
    }
  }, [selectedIssueType]);

  /**
   * Update form data when dynamic fields change
   * @param {Object} newFormData - New form data from child component
   */
  const updateFormValue = (newFormData) => {
    setFormData(newFormData);
  };

  // Memoized SelectOption arrays so JSX icons don't recreate on every render
  const configOptions = useMemo(
    () =>
      (configList || []).map((c) => {
        const iconSrc = getToolIcon(c.tool);
        return {
          value: String(c.id),
          label: c.name,
          icon: iconSrc ? <SafeIcon src={iconSrc} alt={c.tool} width={16} height={16} style={{ objectFit: 'contain' }} /> : undefined,
        };
      }),
    [configList]
  );

  const projectOptions = useMemo(() => (projectList || []).map((p) => ({ value: p.key, label: p.name })), [projectList]);

  const projectLabel =
    selectedConfig?.tool === 'jira'
      ? 'Select Jira Project'
      : selectedConfig?.tool === 'servicenow'
      ? 'Select Table'
      : selectedConfig?.tool === 'pagerduty'
      ? 'Select PagerDuty Service'
      : selectedConfig?.tool === 'zenduty'
      ? 'Select ZenDuty Service'
      : selectedConfig?.tool === 'gitlab'
      ? 'Select GitLab Project'
      : 'Select Github Repository';

  return (
    <Form variant='stacked' density='compact'>
      {/* Ticket Configuration — half-width to preserve original layout. */}
      <Form.Row ratio={[1, 1]}>
        <Form.Field label='Select Ticket Config' required error={getFieldErrorMessage('Ticket Config', selectedConfig)}>
          <Select
            id='config'
            disabled={viewOnlyMode}
            options={configOptions}
            value={selectedConfig ? String(selectedConfig.id) : null}
            onChange={(id) => {
              const found = configList.find((c) => String(c.id) === id);
              setSelectedConfig(found ?? null);
              setSelectedProject(null);
              setSelectedIssueType('');
              setFormData({});
            }}
          />
        </Form.Field>
        <Box />
      </Form.Row>

      {/* Project + Issue Type pair — visible once a config is selected. */}
      {selectedConfig && (
        <Form.Row ratio={[1, 1]}>
          <Form.Field label={projectLabel} required error={getFieldErrorMessage(projectLabel, selectedProject)}>
            <Select
              id='projectKey'
              disabled={!(selectedConfig && Object.keys(selectedConfig).length > 0) || viewOnlyMode}
              options={projectOptions}
              value={selectedProject?.key ?? null}
              onChange={(key) => {
                const found = projectList.find((p) => p.key === key);
                setSelectedProject(found ?? null);
              }}
            />
          </Form.Field>
          <Form.Field label='Select Issue' required error={getFieldErrorMessage('Issue Type', selectedIssueType)}>
            <Select
              id='issue'
              disabled={!selectedProject || issueTypes.length === 0 || viewOnlyMode || loadingIssueTypes}
              options={issueTypes}
              value={selectedIssueType || null}
              placeholder={loadingIssueTypes ? 'Loading...' : 'Select…'}
              onChange={(v) => {
                setSelectedIssueType(v);
                setFormData({});
                setSelectedIssueTypeTicketMetadata([]);
              }}
            />
          </Form.Field>
        </Form.Row>
      )}

      {/* Dynamic metadata-driven fields — only when issue type is selected. */}
      {selectedConfig && selectedIssueTypeTicketMetadata && selectedIssueTypeTicketMetadata.length > 0 && (
        <TicketFormComponent
          configurationId={selectedConfig?.id}
          fields={selectedIssueTypeTicketMetadata[0].fields || {}}
          initialValues={formData}
          onChanges={updateFormValue}
          forceValidate={forceValidate}
          viewOnlyMode={viewOnlyMode}
        />
      )}

      {/* Subject */}
      {!ignoreFields.includes('subject') && (
        <Form.Field label='Subject' required error={getFieldErrorMessage('Subject', ticketDetails.subject)}>
          <Input
            id='subject'
            value={ticketDetails.subject}
            size='sm'
            disabled={viewOnlyMode}
            onChange={(next) => {
              setTicketDetails({ ...ticketDetails, subject: next });
            }}
          />
        </Form.Field>
      )}

      {/* Description editor */}
      {!ignoreFields.includes('description') && (
        <TicketsDescriptionEditor
          error={forceValidate}
          value={ticketDetails?.description}
          issueUrl={ticketUrl?.url ?? ''}
          viewOnlyMode={viewOnlyMode}
          onChange={(newDescription) => {
            setTicketDetails({ ...ticketDetails, description: newDescription });
          }}
        />
      )}
    </Form>
  );
};

TicketFormSection.propTypes = {
  ticketUrl: PropTypes.shape({
    url: PropTypes.string,
  }),
  ticketData: PropTypes.shape({
    subject: PropTypes.string,
    description: PropTypes.string,
    selectedConfig: PropTypes.object,
    projectKey: PropTypes.string,
    ticketType: PropTypes.string,
    additionalFields: PropTypes.object,
    selectedProject: PropTypes.object,
  }),
  error: PropTypes.bool,
  onStateChange: PropTypes.func,
  ignoreFields: PropTypes.array,
  isEdit: PropTypes.bool,
  forceValidate: PropTypes.bool,
  viewOnlyMode: PropTypes.bool,
  toolName: PropTypes.string,
};

export default TicketFormSection;
