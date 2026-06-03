import React, { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { Button as DsButton } from '@components1/ds/Button';
import { Chip as DsChip } from '@components1/ds/Chip';
import { Select as DsSelect } from '@components1/ds/Select';
import VerticalStepNavigation from '@components1/common/VerticalStepNavigation';
import TaskAccordion from './TaskAccordion';
import { toast as snackbar } from '@components1/ds/Toast';
import apiKubernetes1 from '@api1/kubernetes1';
import EmptyData from '@components1/common/EmptyData';
import { DataNotAvailable, aapRightArrow } from '@assets';
import Loader from '@components1/common/Loader';
import { hasFeatureAccess, hasWriteAccess } from '@lib/auth';
import { Card } from '@components1/ds/Card';
import CreatePlanConfirmationModal from './CreatePlanConfirmationModal';
import SafeIcon from '@components1/common/SafeIcon';
import { ds } from '@utils/colors';

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

interface KubernetesClusterUpgradePlannerProps {
  accountId?: string;
  // accountId is not used currently but kept for future API integration
}

interface UpgradePlan {
  id: string;
  created_at: string;
  updated_at: string;
  current_version: string;
  target_version: string;
  owner: string;
  k8s_provider: string;
  account_id: string;
  tenant_id: string;
  status: string;
  steps: UpgradeStep[];
}

const KubernetesClusterUpgradePlanner: React.FC<KubernetesClusterUpgradePlannerProps> = ({ accountId }) => {
  const [activeStep, setActiveStep] = useState(1);
  const [activeTask, setActiveTask] = useState<string>('');
  const [isCreatingPlan, setIsCreatingPlan] = useState(false);
  const [showConfirmationModal, setShowConfirmationModal] = useState(false);

  // New state for multiple plans
  const [allPlans, setAllPlans] = useState<UpgradePlan[]>([]);
  const [selectedPlanId, setSelectedPlanId] = useState<string | null>(null);

  const [clusterInfo, setClusterInfo] = useState<{
    current_version: string;
    target_version: string;
    k8s_provider: string;
    created_at: string;
    updated_at: string;
    plan_id?: string;
  }>({
    current_version: '',
    target_version: '',
    k8s_provider: '',
    created_at: '',
    updated_at: '',
    plan_id: '',
  });

  const [upgradeSteps, setUpgradeSteps] = useState<UpgradeStep[]>();
  const [isLoading, setIsLoading] = useState(true);

  // M6: Export plan as JSON file
  const handleExportPlan = () => {
    if (!upgradeSteps || !clusterInfo?.plan_id) return;

    const exportData = {
      plan_id: clusterInfo.plan_id,
      current_version: clusterInfo.current_version,
      target_version: clusterInfo.target_version,
      k8s_provider: clusterInfo.k8s_provider,
      created_at: clusterInfo.created_at,
      updated_at: clusterInfo.updated_at,
      steps: upgradeSteps.map((step) => ({
        sequence: step.sequence,
        title: step.title,
        status: step.status,
        tasks: step.tasks.map((task) => ({
          title: task.title,
          status: task.status,
          description: task.description,
          owner: task.owner || '',
          is_required: task.is_required,
        })),
      })),
    };

    const blob = new Blob([JSON.stringify(exportData, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = `upgrade-plan-${clusterInfo.current_version}-to-${clusterInfo.target_version}.json`;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
    URL.revokeObjectURL(url);
  };

  const handleStepChange = (step: number, _id: string) => {
    setActiveStep(step);
    // Auto-select first task when step changes
    if (upgradeSteps && upgradeSteps[step - 1]?.tasks?.length > 0) {
      setActiveTask(upgradeSteps[step - 1].tasks[0].id);
    }
  };

  const handleTaskChange = (stepId: string, taskId: string) => {
    setActiveTask(taskId);
    // Also set the active step to the step containing this task
    const stepIndex = upgradeSteps?.findIndex((step) => step.id === stepId);
    if (stepIndex !== undefined && stepIndex >= 0) {
      setActiveStep(stepIndex + 1);
    }
  };

  const handleCreatePlanClick = () => {
    setShowConfirmationModal(true);
  };

  // Helper function to load a specific plan into the UI
  const loadPlan = (plan: UpgradePlan) => {
    setUpgradeSteps(plan.steps);
    setClusterInfo({
      current_version: plan.current_version,
      target_version: plan.target_version,
      k8s_provider: plan.k8s_provider,
      created_at: plan.created_at,
      updated_at: plan.updated_at,
      plan_id: plan.id,
    });
    setSelectedPlanId(plan.id);
    setActiveStep(1);
    setActiveTask('');
  };

  const handleConfirmCreatePlan = async () => {
    setShowConfirmationModal(false);
    setIsCreatingPlan(true);
    setUpgradeSteps([]); // Clear existing steps when creating a new plan
    setIsLoading(true);
    try {
      const response = await apiKubernetes1.generateUpgradePlan(accountId as string);
      if (response?.errors?.length) {
        snackbar.error('Failed to create upgrade plan. Please try again later.');
        setIsCreatingPlan(false);
        return;
      }

      // Start polling for the upgrade plan
      const pollForUpgradePlan = async () => {
        try {
          const pollResponse = await apiKubernetes1.getUpgradePlans(accountId as string);
          if (pollResponse?.errors?.length) {
            return null;
          }

          const plans = pollResponse.data.upgrade_plan;
          // Check if we have plans (sort by created_at desc to get the latest)
          if (plans && plans.length > 0) {
            const sortedPlans = [...plans].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime());
            const latestPlan = sortedPlans[0];
            if (latestPlan?.id) {
              return { plans: sortedPlans, latestPlan };
            }
          }
          return null;
        } catch (error) {
          console.error('Error polling upgrade plan:', error);
          return null;
        }
      };

      const startPolling = async () => {
        // First poll immediately
        const result = await pollForUpgradePlan();
        if (result) {
          setIsCreatingPlan(false);
          setIsLoading(false);

          // Update all plans and load the latest one
          setAllPlans(result.plans);
          loadPlan(result.latestPlan);

          snackbar.success('Upgrade plan created successfully!');
          return;
        }

        // If first poll didn't return data, start interval polling
        const pollInterval = setInterval(async () => {
          const result = await pollForUpgradePlan();
          if (result) {
            clearInterval(pollInterval);
            setIsCreatingPlan(false);
            setIsLoading(false);

            // Update all plans and load the latest one
            setAllPlans(result.plans);
            loadPlan(result.latestPlan);

            snackbar.success('Upgrade plan created successfully!');
          }
        }, 10000); // Poll every 10 seconds

        // Optional: Add a timeout to stop polling after a certain time (e.g., 5 minutes)
        setTimeout(() => {
          clearInterval(pollInterval);
          if (isCreatingPlan) {
            setIsCreatingPlan(false);
            setIsLoading(false);
            snackbar.error('Upgrade plan creation timed out. Please try again.');
          }
        }, 300000); // 5 minutes timeout
      };

      startPolling();
    } catch (error) {
      console.error('Error creating upgrade plan:', error);
      snackbar.error('Failed to create upgrade plan. Please try again later.');
      setIsCreatingPlan(false);
    }
  };

  // Fetch all upgrade plans for this account
  useEffect(() => {
    if (!accountId) {
      return;
    }

    setIsLoading(true);
    apiKubernetes1
      .getUpgradePlans(accountId)
      .then((response: any) => {
        if (response?.errors?.length) {
          snackbar.error('Failed to fetch upgrade plans. Please try again later.');
          setUpgradeSteps([]);
          setAllPlans([]);
          setIsLoading(false);
          return;
        }

        const plans = response.data.upgrade_plan || [];

        if (plans.length === 0) {
          setUpgradeSteps([]);
          setAllPlans([]);
          setIsLoading(false);
          return;
        }

        // Sort plans by created_at descending (newest first)
        const sortedPlans = [...plans].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime());
        setAllPlans(sortedPlans);

        // Load the most recent plan by default
        loadPlan(sortedPlans[0]);
        setIsLoading(false);
      })
      .catch((error) => {
        console.error('Failed to fetch upgrade plans:', error);
        snackbar.error('Failed to fetch upgrade plans. Please try again later.');
        setUpgradeSteps([]);
        setAllPlans([]);
        setIsLoading(false);
      });
  }, [accountId]);

  // Reset state when account changes
  useEffect(() => {
    setUpgradeSteps([]);
    setAllPlans([]);
    setClusterInfo({ current_version: '', target_version: '', k8s_provider: '', created_at: '', updated_at: '', plan_id: '' });
    setSelectedPlanId(null);
    setActiveStep(1);
    setActiveTask('');
  }, [accountId]);

  // Handler for plan selection
  const handlePlanChange = (planId: string) => {
    const plan = allPlans.find((p) => p.id === planId);
    if (plan) {
      loadPlan(plan);
    }
  };

  // Initialize activeTask when upgradeSteps change
  useEffect(() => {
    if (upgradeSteps && upgradeSteps.length > 0 && upgradeSteps[0]?.tasks?.length > 0 && !activeTask) {
      setActiveTask(upgradeSteps[0].tasks[0].id);
    }
  }, [upgradeSteps, activeTask]);

  const [featureAccess, setFeatureAccess] = useState(true);

  useEffect(() => {
    const checkFeatureAccess = async () => {
      try {
        const hasAccess = await hasFeatureAccess('UPGRADE_PLANNER');
        setFeatureAccess(hasAccess);
      } catch (error) {
        console.error('Error checking feature access:', error);
        setFeatureAccess(false);
      }
    };

    checkFeatureAccess();
  }, []);

  const handleTaskStatusChange = async (stepId: string, taskId: string, newStatus: string) => {
    // Store the original state for potential rollback
    const originalSteps = upgradeSteps;

    try {
      // Optimistically update the UI first for better UX
      setUpgradeSteps((prevSteps) =>
        prevSteps?.map((step) => ({
          ...step,
          tasks: step.tasks.map((task) => (task.id === taskId ? { ...task, status: newStatus } : task)),
        }))
      );

      const response = await apiKubernetes1.setUpgradePlanTaskStatus(accountId as string, clusterInfo?.plan_id as string, stepId, taskId, newStatus);
      if (response?.errors?.length) {
        snackbar.error('Failed to update task status. Please try again later.');

        // Revert to the original state on failure
        setUpgradeSteps(originalSteps);
        return;
      }
    } catch (error) {
      console.error('Error updating task status:', error);
      snackbar.error('Failed to update task status. Please try again later.');

      // Revert to the original state on error
      setUpgradeSteps(originalSteps);
    }
  };

  const handleTaskOwnerChange = async (stepId: string, taskId: string, newOwner: string) => {
    // Store the original state for potential rollback
    const originalSteps = upgradeSteps;

    try {
      // Optimistically update the UI first for better UX
      setUpgradeSteps((prevSteps) =>
        prevSteps?.map((step) => ({
          ...step,
          tasks: step.tasks.map((task) => (task.id === taskId ? { ...task, owner: newOwner } : task)),
        }))
      );

      const response = await apiKubernetes1.setUpgradePlanTaskOwner(accountId as string, clusterInfo?.plan_id as string, stepId, taskId, newOwner);
      if (response?.errors?.length) {
        snackbar.error('Failed to update task owner. Please try again later.');

        // Revert to the original state on failure
        setUpgradeSteps(originalSteps);
        return;
      }
    } catch (error) {
      console.error('Error updating task owner:', error);
      snackbar.error('Failed to update task owner. Please try again later.');

      // Revert to the original state on error
      setUpgradeSteps(originalSteps);
    }
  };

  // Check if the current plan is read-only (not the newest plan)
  const isReadOnlyPlan = Boolean(allPlans.length > 0 && selectedPlanId && allPlans[0].id !== selectedPlanId);

  if (!featureAccess) {
    return (
      <Box
        sx={{
          backgroundColor: 'var(--ds-background-200)',
          display: 'flex',
          flexDirection: 'column',
          borderRadius: '0 0 var(--ds-radius-xl) var(--ds-radius-xl)',
          justifyContent: 'center',
          alignItems: 'center',
          p: ds.space[6],
          minHeight: ds.space.mul(0, 200),
        }}
      >
        <EmptyData img={DataNotAvailable} heading='This feature is restricted' subHeading='You can change this in tenant settings' />
      </Box>
    );
  }

  return (
    <Box
      sx={{
        minHeight: '50vh',
        display: 'flex',
        flexDirection: 'column',
        borderRadius: '0 0 var(--ds-radius-xl) var(--ds-radius-xl)',
      }}
    >
      {/* Header Section - Fixed */}

      <Card
        elevation='raised'
        size='md'
        sx={{
          mb: 'var(--ds-space-3)',
          borderLeft: '8px solid var(--ds-blue-300)',
          paddingLeft: 0,
          borderRadius: 'var(--ds-radius-lg)',
          border: '1px solid var(--ds-gray-200)',
        }}
      >
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            flexWrap: 'wrap',
            gap: ds.space[5],
            maxWidth: ds.space.mul(0, 700),
            mx: 'auto',
          }}
        >
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[5], flexWrap: 'wrap' }}>
            {allPlans.length > 1 && selectedPlanId && (
              <Box sx={{ display: 'flex', alignItems: 'center', pl: 0, gap: ds.space[3] }}>
                <Typography
                  variant='body2'
                  sx={{
                    color: 'var(--ds-gray-600)',
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-regular)',
                    marginLeft: 'var(--ds-space-1)',
                  }}
                >
                  Plan
                </Typography>
                <DsSelect
                  id='plan-selector'
                  size='sm'
                  value={selectedPlanId}
                  onChange={handlePlanChange}
                  minWidth='200px'
                  options={allPlans.map((plan, index) => {
                    const createdDate = new Date(plan.created_at);
                    const diffDays = Math.floor(Math.abs(Date.now() - createdDate.getTime()) / 86_400_000);
                    const dateText =
                      diffDays === 0
                        ? 'Today'
                        : diffDays === 1
                        ? 'Yesterday'
                        : diffDays < 7
                        ? `${diffDays} days ago`
                        : createdDate.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
                    return {
                      value: plan.id,
                      label: (
                        <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', width: '100%', gap: 'var(--ds-space-3)' }}>
                          <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
                            <DsChip size='xs' shape='rect' tone='info' solid={plan.id === selectedPlanId}>{`#${allPlans.length - index}`}</DsChip>
                            <Typography
                              sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}
                            >
                              {`v${plan.current_version} → v${plan.target_version}`}
                            </Typography>
                          </Box>
                          <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: 'var(--ds-gray-500)' }}>{dateText}</Typography>
                        </Box>
                      ),
                    };
                  })}
                />
              </Box>
            )}

            {clusterInfo.k8s_provider ? (
              <Box
                sx={{
                  display: 'flex',
                  borderRadius: ds.radius.md,
                  alignItems: 'center',
                  gap: ds.space[2],
                }}
              >
                <Typography
                  variant='body2'
                  sx={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-regular)' }}
                >
                  Provider
                </Typography>
                <DsChip size='sm' shape='rect' tone='info'>
                  {clusterInfo.k8s_provider}
                </DsChip>
              </Box>
            ) : null}

            {/* Read-only indicator for older plans */}
            {allPlans.length > 0 && selectedPlanId && allPlans[0].id !== selectedPlanId && (
              <DsChip size='sm' shape='rect' tone='warning'>
                Read-only
              </DsChip>
            )}
          </Box>

          {clusterInfo.current_version && clusterInfo.target_version ? (
            <Box
              sx={{
                display: 'flex',
                alignItems: 'center',
                gap: ds.space[5],
                borderRadius: ds.radius.lg,
              }}
            >
              <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[3] }}>
                <Typography
                  variant='body2'
                  sx={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-regular)' }}
                >
                  Current
                </Typography>
                <DsChip size='sm' shape='rect' tone='neutral'>{`v${clusterInfo.current_version}`}</DsChip>
              </Box>
              <Box
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'center',
                  border: '1px solid var(--ds-blue-300)',
                  borderRadius: 'var(--ds-radius-xl)',
                  padding: 'var(--ds-space-1) var(--ds-space-1)',
                }}
              >
                <SafeIcon src={aapRightArrow} alt='arrow' width={20} height={20} style={{ opacity: 0.6 }} />
              </Box>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[3] }}>
                <Typography
                  variant='body2'
                  sx={{ color: 'var(--ds-gray-500)', fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-regular)' }}
                >
                  Target
                </Typography>
                <DsChip size='sm' shape='rect' tone='success'>{`v${clusterInfo.target_version}`}</DsChip>
              </Box>
            </Box>
          ) : (
            <Box />
          )}

          <Box sx={{ display: 'flex', gap: ds.space[2], alignItems: 'center' }}>
            {upgradeSteps && upgradeSteps.length > 0 && (
              <DsButton tone='secondary' size='md' onClick={handleExportPlan} data-testid='export-plan-btn'>
                Export Plan
              </DsButton>
            )}
            {hasWriteAccess(accountId) && (
              <DsButton tone='primary' size='md' onClick={handleCreatePlanClick} loading={isCreatingPlan} data-testid='create-plan-btn'>
                Create Plan
              </DsButton>
            )}
          </Box>
        </Box>
      </Card>

      {/* Main Content Area */}
      <Box sx={{ flex: 1, width: '100%' }}>
        {upgradeSteps?.length && upgradeSteps.length > 0 ? (
          <Box
            sx={{
              display: 'flex',
              height: 'calc(100vh - 140px)',
              overflow: 'hidden',
              justifyContent: 'space-between',
            }}
          >
            {/* Left Sidebar - 20% width */}
            <Box sx={{ width: '20%' }}>
              <VerticalStepNavigation
                steps={upgradeSteps}
                activeStep={activeStep}
                activeTask={activeTask}
                onStepChange={handleStepChange}
                onTaskChange={handleTaskChange}
                showTasks={true}
              />
            </Box>

            {/* Right Content Area - 80% width */}
            <Box
              sx={{
                width: '78%',
                display: 'flex',
                flexDirection: 'column',
                backgroundColor: 'var(--ds-background-100)',
                border: '1px solid var(--ds-gray-200)',
                borderRadius: 'var(--ds-radius-xl)',
                overflowY: 'auto',
                '&::-webkit-scrollbar': {
                  width: ds.space.mul(0, 3),
                },
                '&::-webkit-scrollbar-track': {
                  background: 'transparent',
                },
                '&::-webkit-scrollbar-thumb': {
                  background: 'var(--ds-gray-300)',
                  borderRadius: 'var(--ds-radius-sm)',
                  '&:hover': {
                    background: 'var(--ds-brand-200)',
                  },
                },
                '&::-webkit-scrollbar-thumb:active': {
                  background: 'var(--ds-gray-400)',
                },
              }}
            >
              <Box>
                {(() => {
                  // Find the currently selected task
                  let selectedTask = null;
                  let selectedStep = null;

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

                  if (!selectedTask || !selectedStep) {
                    return (
                      <Box
                        sx={{
                          display: 'flex',
                          justifyContent: 'center',
                          alignItems: 'center',
                          minHeight: ds.space.mul(0, 100),
                          flexDirection: 'column',
                          gap: ds.space[4],
                        }}
                      >
                        <Typography variant='h6' sx={{ color: 'var(--ds-gray-600)' }}>
                          Select a task to view details
                        </Typography>
                        <Typography variant='body2' sx={{ color: 'var(--ds-gray-600)' }}>
                          Choose a task from the left sidebar to see its content
                        </Typography>
                      </Box>
                    );
                  }

                  return (
                    <Box sx={{ width: '100%' }}>
                      {/* Task Content */}
                      <TaskAccordion
                        activeTask={selectedTask.id}
                        upgradeSteps={upgradeSteps}
                        clusterInfo={clusterInfo}
                        accountId={accountId}
                        handleTaskStatusChange={handleTaskStatusChange}
                        handleTaskOwnerChange={handleTaskOwnerChange}
                        isReadOnly={isReadOnlyPlan}
                      />
                    </Box>
                  );
                })()}
              </Box>
            </Box>
          </Box>
        ) : isLoading ? (
          <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: ds.space.mul(0, 200) }}>
            <Loader style={{ height: '100%', width: '100%' }} />
          </Box>
        ) : (
          <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: ds.space.mul(0, 200), p: ds.space[6] }}>
            <EmptyData img={DataNotAvailable} heading='No upgrade plan available' subHeading="Please click on 'Create Plan' button to generate one" />
          </Box>
        )}
      </Box>

      <CreatePlanConfirmationModal
        open={showConfirmationModal}
        handleClose={() => setShowConfirmationModal(false)}
        onConfirm={handleConfirmCreatePlan}
        isLoading={isCreatingPlan}
      />
    </Box>
  );
};

export default KubernetesClusterUpgradePlanner;
