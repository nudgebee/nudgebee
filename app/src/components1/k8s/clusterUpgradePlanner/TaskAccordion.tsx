import React, { useEffect, useState } from 'react';
import { Box, Typography, Avatar, IconButton, Button, Chip, Select, MenuItem, FormControl, type SelectChangeEvent } from '@mui/material';
import { colors } from 'src/utils/colors';
import HistoryIcon from '@mui/icons-material/History';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import MarkDowns from '@components1/common/MarkDowns';
import HighLights from '@components1/common/widgets/HighLights';
import SafeIcon from '@components1/common/SafeIcon';
import SparklesIcon from '@assets/kubernetes/sparkle.svg';
import {
  PdbContent,
  HelmContent,
  AddOnContent,
  KubeProxyContent,
  DeprecatedApisContent,
  ClusterHealthWorkloadsContent,
  ClusterHealthServicesContent,
  ClusterHealthNodesContent,
  ClusterHealthPvContent,
  ClusterHealthLoadBalancerContent,
  ClusterHealthNodeGroupsContent,
  PreFlightCheckContent,
  PostFlightCheckContent,
} from './TaskContentComponents';
import Datetime from '@components1/common/format/Datetime';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import apiUser from '@api1/user';
import FilterDropdownButton from '@components1/common/FilterDropdownButton';
import CustomTooltip from '@components1/common/CustomTooltip';
import { Modal } from '@components1/common/modal';
import apiKubernetes1 from '@api1/kubernetes1';
import { snackbar } from '@components1/common/snackbarService';
import { hasWriteAccess } from '@lib/auth';

interface Task {
  id: string;
  title: string;
  description: string;
  status: string;
  action?: string;
  resource_type?: string;
  owner?: string;
  is_required?: boolean;
}

interface UpgradeStep {
  sequence: number;
  title: string;
  description: string;
  status: string;
  tasks: Task[];
  id: string;
}

interface TaskAccordionProps {
  // Props for right content area
  activeTask?: string;
  upgradeSteps?: UpgradeStep[];
  clusterInfo?: {
    current_version: string;
    target_version: string;
    k8s_provider: string;
    created_at: string;
    updated_at: string;
    plan_id?: string;
  };
  accountId?: string;
  handleTaskStatusChange?: (stepId: string, taskId: string, newStatus: string) => Promise<void> | void;
  handleTaskOwnerChange?: (stepId: string, taskId: string, newOwner: string) => Promise<void> | void;
  isReadOnly?: boolean;
}

interface StatusOption {
  value: string;
  label: string;
  variant: string;
}

interface AuditEntry {
  id: string;
  action: string;
  actioned_by: string;
  new_value: string;
  old_value: string;
  field: string;
  created_at: string;
  userActionedBy?: {
    display_name: string;
  };
}

const statusOptions: StatusOption[] = [
  { value: 'pending', label: 'PENDING', variant: 'yellow' },
  { value: 'skipped', label: 'SKIPPED', variant: 'grey' },
  { value: 'completed', label: 'COMPLETED', variant: 'green' },
  { value: 'failed', label: 'FAILED', variant: 'red' },
];

const TaskAccordion: React.FC<TaskAccordionProps> = ({
  activeTask,
  upgradeSteps,
  clusterInfo,
  accountId,
  handleTaskStatusChange,
  handleTaskOwnerChange,
  isReadOnly = false,
}) => {
  // Find the currently selected task
  let selectedTask: Task | null = null;
  let selectedStep: UpgradeStep | null = null;

  if (activeTask && upgradeSteps) {
    for (const step of upgradeSteps) {
      const task = step.tasks.find((t) => t.id === activeTask);
      if (task) {
        selectedTask = task;
        selectedStep = step;
        break;
      }
    }
  }

  // State for TaskAccordionContent functionality
  const [insights, setInsights] = useState<any[]>([]);
  const [auditDialogOpen, setAuditDialogOpen] = useState(false);
  const [loadingAudit, setLoadingAudit] = useState(false);
  const [auditTableData, setAuditTableData] = useState<any[]>([]);
  const [currentOwnerOption, setCurrentOwnerOption] = useState<{ label: string; value: string } | null>(null);
  const [currentStatus, setCurrentStatus] = useState<string>(selectedTask?.status.toLowerCase() || '');

  // State for command execution popup
  const [commandPopupOpen, setCommandPopupOpen] = useState(false);
  const [commandResults, setCommandResults] = useState<any>(null);
  const [commandLoading, setCommandLoading] = useState(false);
  const [executedCommand, setExecutedCommand] = useState<string>('');

  const tableHeaders: Array<{ name: string; width: string }> = [
    { name: 'Actioned By', width: '20%' },
    { name: 'Action', width: '20%' },
    { name: 'Field', width: '20%' },
    { name: 'New Value', width: '20%' },
    { name: 'Timestamp', width: '20%' },
  ];

  const [allUsers, setAllUsers] = useState<Array<{ label: string; value: string }>>([]);

  const fetchAllUsers = async () => {
    const params = { status: 'active' };
    apiUser.listUsers(params).then((res) => {
      const userOptions = res?.data
        ?.filter((m: any) => m.display_name != '')
        ?.map((u: any) => ({
          label: u.display_name,
          value: u.id,
        }))
        ?.filter((user: any, index: number, self: any[]) => index === self.findIndex((u: any) => u.label === user.label));
      setAllUsers(userOptions);
    });
  };

  useEffect(() => {
    fetchAllUsers();
  }, []);

  useEffect(() => {
    setInsights([]);
  }, [activeTask]);
  const updateTaskOwner = async (taskId: string, newOwner: string) => {
    return new Promise((resolve) => {
      setTimeout(() => {
        resolve({ success: true, data: { taskId, owner: newOwner } });
      }, 500);
    });
  };

  const handleOwnerChange = async (_event: any, option: { label: string; value: string } | null) => {
    const previous = currentOwnerOption;
    setCurrentOwnerOption(option);

    if (selectedTask && selectedStep) {
      try {
        const ownerValue = option?.value ?? '';
        await updateTaskOwner(selectedTask.id, ownerValue);
        if (handleTaskOwnerChange) {
          await handleTaskOwnerChange(selectedStep.id, selectedTask.id, ownerValue);
        }
      } catch (error) {
        console.error('Error updating task owner:', error);
        setCurrentOwnerOption(previous);
      }
    }
  };

  const handleDropdownClick = (e: React.MouseEvent) => {
    e.stopPropagation();
  };

  const handleAuditClick = async () => {
    if (!selectedTask || !accountId) {
      return;
    }

    setLoadingAudit(true);
    setAuditDialogOpen(true);

    try {
      const apiKubernetes1 = await import('@api1/kubernetes1');
      const response = await apiKubernetes1.default.getUpgradePlanTaskAudits(selectedTask.id);
      if (response?.data?.upgrade_plan_audit) {
        // Transform data for CustomTable2
        const transformedData = response.data.upgrade_plan_audit.map((audit: AuditEntry) => {
          return [
            {
              text: audit.userActionedBy?.display_name || 'System',
              component: (
                <Typography sx={{ fontSize: '13px', color: colors.text.secondary }}>{audit.userActionedBy?.display_name || 'System'}</Typography>
              ),
            },
            {
              component: (
                <Chip
                  label={audit.action.toUpperCase()}
                  size='small'
                  sx={{
                    height: '22px',
                    fontSize: '11px',
                    fontWeight: 600,
                    backgroundColor: '#e3f2fd',
                    color: '#1565c0',
                    '& .MuiChip-label': { px: 1 },
                  }}
                />
              ),
              text: audit.action.toUpperCase(),
            },
            {
              text: audit.field.charAt(0).toUpperCase() + audit.field.slice(1),
              component: (
                <Typography sx={{ fontSize: '13px', fontWeight: 500, color: colors.text.primary }}>
                  {audit.field.charAt(0).toUpperCase() + audit.field.slice(1)}
                </Typography>
              ),
            },
            {
              text: audit.new_value,
              component:
                audit.field === 'status' ? (
                  <CustomLabels text={audit.new_value} variant={statusOptions.find((option) => option.value == audit.new_value)?.variant || 'grey'} />
                ) : audit.field === 'owner' ? (
                  <Typography sx={{ fontSize: '13px' }}>
                    {allUsers.find((user) => user.value === audit.new_value)?.label || audit.new_value}
                  </Typography>
                ) : (
                  <Typography sx={{ fontSize: '13px', color: colors.text.primary }}>{audit.new_value}</Typography>
                ),
            },
            {
              text: <Datetime value={audit.created_at} />,
              component: (
                <Typography sx={{ fontSize: '12px', color: colors.text.secondary, fontFamily: 'monospace' }}>
                  <Datetime value={audit.created_at} />
                </Typography>
              ),
            },
          ];
        });
        setAuditTableData(transformedData);
      } else {
        setAuditTableData([]);
      }
    } catch (error) {
      console.error('Error fetching audit data:', error);
      setAuditTableData([]);
    } finally {
      setLoadingAudit(false);
    }
  };

  const handleStatusChange = async (event: SelectChangeEvent<string>) => {
    const newStatus = event.target.value as string;
    const previousStatus = currentStatus;
    setCurrentStatus(newStatus);
    if (handleTaskStatusChange && selectedTask && selectedStep) {
      try {
        await handleTaskStatusChange(selectedStep.id, selectedTask.id, newStatus);
      } catch (error) {
        console.error('Error updating task status:', error);
        setCurrentStatus(previousStatus);
      }
    }
  };

  useEffect(() => {
    const owner = selectedTask?.owner;
    const found = allUsers.find((u) => u.value === owner);
    setCurrentOwnerOption(found ?? (owner ? { label: owner, value: owner } : null));
  }, [selectedTask?.owner, allUsers]);

  useEffect(() => {
    setCurrentStatus(selectedTask?.status.toLowerCase() || '');
  }, [selectedTask?.status]);

  // Function to determine command type based on command content
  const determineCommandType = (command: string): string => {
    const lowerCommand = command.toLowerCase().trim();

    if (lowerCommand.includes('kubectl')) {
      return 'kubectl';
    } else if (lowerCommand.includes('aws')) {
      return 'aws';
    }
    return '';
  };

  // Function to handle command execution
  const handleCommandExecution = async (command: string) => {
    if (!selectedTask || !selectedStep || !clusterInfo?.plan_id || !accountId) {
      console.error('Missing required data for command execution');
      return;
    }

    setCommandLoading(true);
    setCommandPopupOpen(true);
    setCommandResults(null);
    setExecutedCommand(command);

    try {
      const commandType = determineCommandType(command);

      const response = await apiKubernetes1.executeClusterUpgradePlannerCommand(
        accountId,
        command,
        commandType,
        clusterInfo.plan_id,
        selectedStep.id,
        selectedTask.id
      );

      // Check for errors in the response
      if (response?.data?.errors && response.data.errors.length > 0) {
        const errorMessages = response.data.errors.map((err: any) => err.message || err).join(', ');
        snackbar.error('Error executing command ');
        setCommandResults({
          success: false,
          error: errorMessages,
          output: 'Command execution failed',
        });
      } else {
        // Success case - extract the actual command results
        const commandData = response?.data?.data?.upgrade_execute_command || response?.data?.upgrade_execute_command;
        setCommandResults(
          commandData || {
            success: false,
            error: 'No command results received',
            output: 'Empty response from server',
          }
        );
      }
    } catch (error) {
      console.error('Error executing command:', error);
      setCommandResults({
        success: false,
        error: 'Failed to execute command',
        output: 'Unknown error occurred',
      });
    } finally {
      setCommandLoading(false);
    }
  };

  if (!selectedTask) {
    return (
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
          backgroundColor: colors.background.white,
          p: 3,
        }}
      >
        <Typography variant='body1' sx={{ color: colors.text.secondary, textAlign: 'center' }}>
          Please select a task to view details.
        </Typography>
      </Box>
    );
  }

  return (
    <>
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'column',
        }}
      >
        <Box sx={{ flex: 1, p: '24px 28px', overflowY: 'auto' }}>
          {/* Task Header */}
          <Box sx={{ mb: 1, pb: 2, borderBottom: `1px solid ${colors.border.secondaryLightest}` }}>
            <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-start' }}>
                {/* Step Title and Count */}
                {selectedStep && (
                  <Typography
                    sx={{
                      fontSize: '12px',
                      fontWeight: 400,
                      color: colors.text.tertiary,
                      fontFamily: 'poppins',
                      letterSpacing: '-0.01em',
                    }}
                  >
                    {selectedStep.sequence}: {selectedStep.title} {' > '}
                  </Typography>
                )}

                {/* Task Title */}
                <Typography
                  sx={{
                    fontSize: '18px',
                    fontWeight: 600,
                    color: colors.text.secondary,
                    fontFamily: 'poppins',
                    letterSpacing: '-0.025em',
                  }}
                >
                  {selectedTask.title}
                  {selectedTask.is_required !== false && (
                    <Typography
                      component='span'
                      sx={{
                        color: 'rgb(239, 108, 108)',
                        fontSize: '14px',
                        fontWeight: 400,
                        ml: 0.5,
                      }}
                    >
                      (required)
                    </Typography>
                  )}
                </Typography>
              </Box>

              <Box sx={{ display: 'flex', alignItems: 'center', gap: 1, flexShrink: 0 }}>
                {hasWriteAccess(accountId) && (
                  <>
                    {/* Status Dropdown */}
                    {handleTaskStatusChange && (
                      <Box sx={{ zIndex: 2 }} onClick={handleDropdownClick}>
                        <FormControl size='small'>
                          <Select
                            value={currentStatus}
                            onChange={handleStatusChange}
                            disabled={isReadOnly}
                            renderValue={(value) => (
                              <CustomLabels
                                text={value}
                                variant={statusOptions.find((option) => option.value == value)?.variant || 'grey'}
                                height='28px'
                                showDropdownArrow={!isReadOnly}
                              />
                            )}
                            sx={{
                              height: '32px',
                              opacity: isReadOnly ? 0.6 : 1,
                              cursor: isReadOnly ? 'not-allowed' : 'pointer',
                              '& .MuiOutlinedInput-notchedOutline': {
                                border: 'none',
                              },
                              '& .MuiSelect-select': {
                                p: 0,
                                paddingRight: '0px !important',
                                display: 'flex',
                                alignItems: 'center',
                              },
                              '& .MuiSelect-icon': {
                                display: 'none',
                              },
                            }}
                          >
                            {statusOptions.map((option) => (
                              <MenuItem
                                key={option.value}
                                value={option.value}
                                sx={{
                                  p: 1,
                                  justifyContent: 'center',
                                }}
                              >
                                <CustomLabels text={option.value} variant={option.variant} height='24px' />
                              </MenuItem>
                            ))}
                          </Select>
                        </FormControl>
                      </Box>
                    )}

                    <Box sx={{ width: '1px', height: '28px', backgroundColor: colors.border.secondaryLightest, margin: '0px 8px' }} />

                    {/* Owner Dropdown */}
                    <CustomTooltip title={isReadOnly ? 'Editing disabled for older plans' : ''} placement='bottom'>
                      <Box sx={{ display: 'flex', alignItems: 'center', minWidth: '140px', zIndex: 1, height: '28px' }} onClick={handleDropdownClick}>
                        <FilterDropdownButton
                          label='Assign Owner'
                          value={currentOwnerOption}
                          options={allUsers}
                          onSelect={handleOwnerChange}
                          disabled={isReadOnly}
                        />
                      </Box>
                    </CustomTooltip>
                  </>
                )}

                <Box sx={{ width: '1px', height: '28px', backgroundColor: colors.border.secondaryLightest, margin: '0px 8px' }} />

                {/* Audit History Button */}
                <CustomTooltip title='View audit history' placement='bottom'>
                  <IconButton
                    onClick={handleAuditClick}
                    size='small'
                    sx={{
                      color: colors.text.secondary,
                      minWidth: '28px',
                      width: '28px',
                      height: '28px',
                      borderRadius: '6px',
                      opacity: 0.6,
                      flexShrink: 0,
                      '&:hover': {
                        backgroundColor: 'rgba(0, 0, 0, 0.04)',
                      },
                    }}
                  >
                    <HistoryIcon fontSize='small' />
                  </IconButton>
                </CustomTooltip>
              </Box>
            </Box>
          </Box>

          {/* Task Content */}
          <Box
            sx={{
              width: '99%',
              backgroundColor: colors.background.white,
            }}
          >
            {insights.length > 0 && (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: '2px', minWidth: 0, marginTop: '12px' }}>
                {insights.map((insight, index) => (
                  <Box key={index} sx={{ display: 'flex', alignItems: 'center', gap: '6px', minWidth: 0 }}>
                    <Avatar sx={{ width: '16px', height: '16px', bgcolor: 'transparent' }}>
                      <SafeIcon src={SparklesIcon} alt='sparkles-icon' priority={true} />
                    </Avatar>
                    <HighLights
                      text={insight.message}
                      component={insight.component}
                      containerStyles={{ padding: 0, flex: 1, minWidth: 0, overflow: 'hidden' }}
                      styles={{
                        color: insight?.severity === 'Critical' ? '#EF4444' : '#374151',
                        fontSize: '12px',
                        fontWeight: '400',
                        wordBreak: 'break-all',
                      }}
                    />
                  </Box>
                ))}
              </Box>
            )}

            {/* Content Section - Routed by action + resource_type (H1 refactor) */}
            <Box sx={{ mt: 2 }}>
              {(() => {
                const action = selectedTask.action;
                const resourceType = selectedTask.resource_type;

                // Route by action + resource_type for structured task matching
                if (action === 'check_cluster_health' && resourceType) {
                  switch (resourceType) {
                    case 'nodes':
                      return <ClusterHealthNodesContent accountId={accountId} />;
                    case 'workloads':
                      return <ClusterHealthWorkloadsContent accountId={accountId} />;
                    case 'services':
                      return <ClusterHealthServicesContent accountId={accountId} />;
                    case 'persistentvolumes':
                      return <ClusterHealthPvContent accountId={accountId} />;
                    case 'node_groups':
                      return <ClusterHealthNodeGroupsContent accountId={accountId} />;
                    case 'load_balancer':
                      return <ClusterHealthLoadBalancerContent accountId={accountId} />;
                  }
                }

                // Route by action for compatibility/flight checks
                switch (action) {
                  case 'deprecated_api_check':
                    return <DeprecatedApisContent accountId={accountId} targetVersion={clusterInfo?.target_version} onInsightsChange={setInsights} />;
                  case 'pdb_check':
                    return <PdbContent accountId={accountId} onInsightsChange={setInsights} />;
                  case 'helm_compatibility_check':
                    return <HelmContent accountId={accountId} onInsightsChange={setInsights} />;
                  case 'add_on_check':
                    return <AddOnContent accountId={accountId} onInsightsChange={setInsights} />;
                  case 'kube_proxy_check':
                    return <KubeProxyContent accountId={accountId} onInsightsChange={setInsights} />;
                  case 'upgrade_pre_flight_check':
                    return <PreFlightCheckContent accountId={accountId} planId={clusterInfo?.plan_id} />;
                  case 'upgrade_post_flight_check':
                    return <PostFlightCheckContent accountId={accountId} planId={clusterInfo?.plan_id} />;
                  case 'upgrade_execute_command':
                    return selectedTask.description ? (
                      <MarkDowns
                        data={selectedTask.description}
                        sx={{ width: '100%' }}
                        allowExecutable={!isReadOnly ? handleCommandExecution : undefined}
                        canRunCode={hasWriteAccess(accountId) && !isReadOnly}
                        onLinkClick={null}
                      />
                    ) : null;
                  default:
                    // Fallback: title-based matching for backward compatibility with older plans
                    switch (selectedTask.title) {
                      case 'Check Node Health':
                        return <ClusterHealthNodesContent accountId={accountId} />;
                      case 'Check Workload Health':
                        return <ClusterHealthWorkloadsContent accountId={accountId} />;
                      case 'Check Services Health':
                        return <ClusterHealthServicesContent accountId={accountId} />;
                      case 'Check PVs Status':
                        return <ClusterHealthPvContent accountId={accountId} />;
                      case 'Review Node Pool Settings':
                        return <ClusterHealthNodeGroupsContent accountId={accountId} />;
                      case 'Check Load Balancer and Target Instances':
                        return <ClusterHealthLoadBalancerContent accountId={accountId} />;
                      default:
                        return selectedTask.description ? (
                          <MarkDowns
                            data={selectedTask.description}
                            sx={{ width: '100%' }}
                            allowExecutable={undefined}
                            canRunCode={false}
                            onLinkClick={null}
                          />
                        ) : (
                          <Typography variant='body2' sx={{ color: colors.text.secondary, fontStyle: 'italic' }}>
                            No specific content available for this task.
                          </Typography>
                        );
                    }
                }
              })()}
            </Box>
          </Box>
        </Box>
      </Box>

      {/* Command Execution Results Popup */}
      <Modal open={commandPopupOpen} onClose={() => setCommandPopupOpen(false)} title='Command Execution Results' width='lg'>
        <Box sx={{ p: '24px 28px', minHeight: '300px' }}>
          {commandLoading ? (
            <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '200px' }}>
              <Typography
                sx={{
                  fontSize: '14px',
                  color: colors.text.secondary,
                  fontFamily: 'poppins',
                }}
              >
                Executing command...
              </Typography>
            </Box>
          ) : commandResults ? (
            <Box>
              {/* Command Header */}
              <Box sx={{ mb: 3, pb: 2, borderBottom: `1px solid ${colors.border.secondaryLightest}` }}>
                <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', mb: 2 }}>
                  <Typography
                    sx={{
                      fontSize: '16px',
                      fontWeight: 600,
                      color: colors.text.secondary,
                      fontFamily: 'poppins',
                      letterSpacing: '-0.025em',
                    }}
                  />
                  <CustomLabels
                    text={commandResults.success ? 'Success' : 'Failed'}
                    variant={commandResults.success ? 'green' : 'red'}
                    height='24px'
                  />
                </Box>

                {/* Executed Command */}
                <Box sx={{ mb: 2 }}>
                  <Typography
                    sx={{
                      fontSize: '12px',
                      fontWeight: 500,
                      color: colors.text.tertiary,
                      mb: 1,
                      fontFamily: 'poppins',
                      textTransform: 'uppercase',
                      letterSpacing: '0.5px',
                    }}
                  >
                    Executed Command
                  </Typography>
                  <Box
                    sx={{
                      backgroundColor: '#f8fafc',
                      border: '1px solid #e2e8f0',
                      borderRadius: '6px',
                      padding: '8px 12px',
                      fontFamily: '"Roboto Mono", monospace',
                      fontSize: '13px',
                      color: colors.text.secondary,
                      wordBreak: 'break-all',
                    }}
                  >
                    {executedCommand}
                  </Box>
                </Box>

                {commandResults.error && (
                  <Typography
                    variant='body2'
                    sx={{
                      color: '#EF4444',
                      mt: 1,
                      fontSize: '13px',
                      fontFamily: 'poppins',
                    }}
                  >
                    <strong>Error:</strong> {commandResults.error}
                  </Typography>
                )}
              </Box>

              {/* Output Section */}
              {commandResults.output && (
                <Box>
                  <Typography
                    sx={{
                      fontSize: '14px',
                      fontWeight: 500,
                      color: colors.text.secondary,
                      mb: 2,
                      fontFamily: 'poppins',
                    }}
                  >
                    Command Output
                  </Typography>
                  <Box
                    sx={{
                      backgroundColor: '#2d3748',
                      color: '#e2e8f0',
                      padding: '16px 20px',
                      borderRadius: '8px',
                      fontFamily: '"Roboto Mono", monospace',
                      fontSize: '13px',
                      lineHeight: 1.6,
                      whiteSpace: 'pre-wrap',
                      wordWrap: 'break-word',
                      maxHeight: '400px',
                      overflowY: 'auto',
                      border: '1px solid #4a5568',
                      position: 'relative',
                      '&::-webkit-scrollbar': {
                        width: '8px',
                      },
                      '&::-webkit-scrollbar-track': {
                        backgroundColor: '#1a202c',
                        borderRadius: '4px',
                      },
                      '&::-webkit-scrollbar-thumb': {
                        backgroundColor: '#4a5568',
                        borderRadius: '4px',
                        '&:hover': {
                          backgroundColor: '#718096',
                        },
                      },
                    }}
                  >
                    {commandResults.output}
                  </Box>
                </Box>
              )}
            </Box>
          ) : (
            <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '200px' }}>
              <Typography
                sx={{
                  fontSize: '14px',
                  color: colors.text.secondary,
                  fontFamily: 'poppins',
                }}
              >
                No results to display
              </Typography>
            </Box>
          )}
        </Box>

        {/* Footer */}
        <Box
          sx={{
            px: '28px',
            py: '16px',
            borderTop: `1px solid ${colors.border.secondaryLightest}`,
            display: 'flex',
            justifyContent: 'flex-end',
            backgroundColor: colors.background.white,
          }}
        >
          <Button
            onClick={() => setCommandPopupOpen(false)}
            sx={{
              color: colors.text.primary,
              fontWeight: 500,
              px: 3,
              py: 1,
              fontSize: '14px',
              fontFamily: 'poppins',
              textTransform: 'none',
              borderRadius: '6px',
              '&:hover': {
                backgroundColor: 'rgba(0, 0, 0, 0.04)',
              },
            }}
          >
            Close
          </Button>
        </Box>
      </Modal>

      {/* Audit History Popup */}
      <Modal open={auditDialogOpen} onClose={() => setAuditDialogOpen(false)} title='Audit History' width='md'>
        <Box sx={{ py: 3, minHeight: '300px' }}>
          <Typography variant='body2' sx={{ color: colors.text.secondary, mb: 1 }}>
            Task: {selectedTask.title}
          </Typography>
          {loadingAudit ? (
            <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '200px' }}>
              <Typography color='text.secondary'>Loading audit data...</Typography>
            </Box>
          ) : auditTableData.length === 0 ? (
            <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '200px' }}>
              <Typography color='text.secondary'>No audit entries found for this task.</Typography>
            </Box>
          ) : (
            <Box sx={{}}>
              <CustomTable2 tableData={auditTableData as any} headers={tableHeaders as any} loading={loadingAudit} />
            </Box>
          )}
        </Box>
        <Box sx={{ px: 3, py: 2, borderTop: '1px solid #f0f0f0', display: 'flex', justifyContent: 'flex-end' }}>
          <Button
            onClick={() => setAuditDialogOpen(false)}
            sx={{
              color: colors.text.primary,
              fontWeight: 500,
              px: 3,
              '&:hover': {
                backgroundColor: 'rgba(0, 0, 0, 0.04)',
              },
            }}
          >
            Close
          </Button>
        </Box>
      </Modal>
    </>
  );
};

export default TaskAccordion;
