import React, { useEffect, useState } from 'react';
import { Autocomplete, Grid, TextField, CircularProgress, Box } from '@mui/material';
import { inputCustomSx } from '@data/themes/inputField';
import TicketsDescriptionEditor from './TicketsDescriptionEditor';
import apiTickets from '@api1/tickets';
import PropTypes from 'prop-types';
import TicketFormComponent from './TicketFormComponent';
import { JiraIcon, ServiceNowIcon, GithubIcon, GitLabIcon, PagerDutyIcon, ZenDutyIcon } from '@assets';
import InputAdornment from '@mui/material/InputAdornment';
import SafeIcon from '@components1/common/SafeIcon';

const commonStyles = {
  width: '100%',
  maxWidth: '100%',
  overflow: 'hidden',
};

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

  return (
    <Grid container columnSpacing={2} xs={12} mt={2} sx={{ ...commonStyles }}>
      {/* Ticket Configuration Dropdown */}
      <Grid item xs={6}>
        <Autocomplete
          disablePortal
          id='config'
          value={selectedConfig}
          onChange={(_, newValue) => {
            setSelectedConfig(newValue);
            setSelectedProject(null);
            setSelectedIssueType('');
            setFormData({});
          }}
          blurOnSelect={'mouse'}
          disabled={viewOnlyMode}
          sx={{ ...inputCustomSx }}
          options={configList}
          getOptionLabel={(o) => o.name}
          renderOption={(props, option) => {
            const toolIcon = getToolIcon(option.tool);
            return (
              <Box component='li' {...props} sx={{ display: 'flex', alignItems: 'center', gap: 1.5, py: 1 }}>
                {toolIcon && (
                  <Box sx={{ display: 'flex', alignItems: 'center', width: 20, height: 20 }}>
                    <SafeIcon src={toolIcon} alt={option.tool} width={20} height={20} style={{ objectFit: 'contain' }} />
                  </Box>
                )}
                <Box>{option.name}</Box>
              </Box>
            );
          }}
          renderInput={(params) => {
            const toolIcon = selectedConfig?.tool && getToolIcon(selectedConfig.tool);
            return (
              <TextField
                {...params}
                label='Select Ticket Config'
                margin='normal'
                size='small'
                required
                error={forceValidate && !selectedConfig}
                helperText={getFieldErrorMessage('Ticket Config', selectedConfig)}
                InputProps={{
                  ...params.InputProps,
                  startAdornment: toolIcon ? (
                    <InputAdornment position='start'>
                      <Box
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          mr: 0,
                        }}
                      >
                        <SafeIcon src={toolIcon} alt={selectedConfig.tool} width={20} height={20} style={{ objectFit: 'contain' }} />
                      </Box>
                    </InputAdornment>
                  ) : null,
                }}
              />
            );
          }}
        />
      </Grid>

      {/* Render Project and Issue Type selectors only when a config is selected */}
      {selectedConfig ? (
        <>
          {/* Project Dropdown */}
          <Grid item xs={6}>
            <Autocomplete
              disablePortal
              id='projectKey'
              key={`project-key-${selectedProject?.key || ''}`}
              value={selectedProject}
              onChange={(_, newValue) => {
                setSelectedProject(newValue);
              }}
              blurOnSelect={'mouse'}
              sx={{ ...inputCustomSx }}
              options={projectList}
              getOptionLabel={(o) => o.name}
              disabled={!(selectedConfig && Object.keys(selectedConfig)?.length > 0) || viewOnlyMode}
              renderInput={(params) => (
                <TextField
                  {...params}
                  error={forceValidate && !selectedProject}
                  helperText={getFieldErrorMessage(
                    selectedConfig.tool === 'jira'
                      ? 'Jira Project'
                      : selectedConfig.tool === 'servicenow'
                      ? 'Table'
                      : selectedConfig.tool === 'pagerduty'
                      ? 'PagerDuty Service'
                      : selectedConfig.tool === 'zenduty'
                      ? 'ZenDuty Service'
                      : selectedConfig.tool === 'gitlab'
                      ? 'GitLab Project'
                      : 'Github Repository',
                    selectedProject
                  )}
                  label={
                    // Customized label based on ticketing system type
                    selectedConfig.tool === 'jira'
                      ? `Select Jira Project`
                      : selectedConfig.tool === 'servicenow'
                      ? 'Select Table'
                      : selectedConfig.tool === 'pagerduty'
                      ? 'Select PagerDuty Service'
                      : selectedConfig.tool === 'zenduty'
                      ? 'Select ZenDuty Service'
                      : selectedConfig.tool === 'gitlab'
                      ? 'Select GitLab Project'
                      : 'Select Github Repository'
                  }
                  margin='normal'
                  size='small'
                  required
                />
              )}
            />
          </Grid>

          {/* Issue Type Dropdown */}
          <Grid item xs={6}>
            <Autocomplete
              disablePortal
              id='issue'
              value={selectedIssueType || ''}
              onChange={(_, newValue) => {
                setSelectedIssueType(newValue);
                setFormData({}); // Reset form data when issue type changes
                setSelectedIssueTypeTicketMetadata([]);
              }}
              blurOnSelect={'mouse'}
              sx={{ ...inputCustomSx }}
              disabled={!selectedProject || issueTypes.length === 0 || viewOnlyMode}
              options={issueTypes}
              renderInput={(params) => (
                <TextField
                  {...params}
                  error={forceValidate && !selectedIssueType}
                  helperText={getFieldErrorMessage('Issue Type', selectedIssueType)}
                  label={'Select Issue'}
                  margin='normal'
                  size='small'
                  required
                  InputProps={{
                    ...params.InputProps,
                    endAdornment: (
                      <>
                        {/* Loading indicator while fetching issue types */}
                        {loadingIssueTypes ? <CircularProgress color='inherit' size={20} /> : null}
                        {params.InputProps.endAdornment}
                      </>
                    ),
                  }}
                />
              )}
            />
          </Grid>

          {/* Dynamic Fields Component - renders when issue type is selected */}
          {selectedIssueTypeTicketMetadata && selectedIssueTypeTicketMetadata.length > 0 ? (
            <Grid item xs={12}>
              <Box sx={{ ...commonStyles }}>
                <TicketFormComponent
                  configurationId={selectedConfig?.id}
                  fields={selectedIssueTypeTicketMetadata[0].fields || {}}
                  initialValues={formData}
                  onChanges={updateFormValue}
                  forceValidate={forceValidate}
                  viewOnlyMode={viewOnlyMode}
                />
              </Box>
            </Grid>
          ) : null}
        </>
      ) : null}

      {/* Subject Field - only if not explicitly ignored */}
      {!ignoreFields.includes('subject') && (
        <Grid item xs={12} sx={{ ...commonStyles }}>
          <TextField
            id='subject'
            value={ticketDetails.subject}
            label='Subject'
            margin='normal'
            sx={inputCustomSx}
            size='small'
            fullWidth
            disabled={viewOnlyMode}
            onChange={(e) => {
              setTicketDetails({ ...ticketDetails, subject: e.target.value });
            }}
            error={forceValidate && !ticketDetails.subject}
            helperText={getFieldErrorMessage('Subject', ticketDetails.subject)}
            required
          />
        </Grid>
      )}

      {/* Description Editor */}
      {!ignoreFields.includes('description') && (
        <Grid item xs={12} sx={{ ...commonStyles }}>
          <TicketsDescriptionEditor
            error={forceValidate}
            value={ticketDetails?.description}
            issueUrl={ticketUrl?.url ?? ''}
            viewOnlyMode={viewOnlyMode}
            onChange={(newDescription) => {
              setTicketDetails({ ...ticketDetails, description: newDescription });
            }}
          />
        </Grid>
      )}
    </Grid>
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
