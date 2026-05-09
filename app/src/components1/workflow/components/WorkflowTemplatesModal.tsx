import React, { useEffect, useState, useCallback } from 'react';
import { Box } from '@mui/material';
import { Modal } from '@components1/common/modal';
import { Text } from '@components1/common';
import WidgetCard from '@components1/common/WidgetCard';
import CustomTabs from '@components1/common/CustomTabs';
import apiWorkflow from '@api1/workflow';
import { useRouter } from 'next/router';
import { colors } from 'src/utils/colors';
import SafeIcon from '@components1/common/SafeIcon';
import {
  workflowMessagingIcon,
  workflowSubWorkflowIcon,
  workflowFormatterIcon,
  workflowDatabaseIcon,
  workflowWebhookIcon,
  aiAgentIcon,
  TicketBlueIcon,
  coreOpsIcon,
  CloudUploadIcon,
  BarsBlueOutlineIcon,
  NotificationIcon1,
  PlayCircleIcon,
  IntegrationsIcon,
  LLMFunctionIcon,
  RabbitmqIcon,
  RedisLogoIcon,
  GithubIcon,
  ArgocdIcon,
  K8sIcon,
  newAwsLogo,
  ouAzure,
  ouGoogle,
} from '@assets';
import CustomButton from '@components1/common/NewCustomButton';
import Loader from '@components1/common/Loader';

interface WorkflowTemplatesModalProps {
  open: boolean;
  onClose: () => void;
  accountId: string;
  eventSources?: string[];
  alertNames?: string[];
  subjectTypes?: string[];
  eventContext?: Record<string, string>;
  onCreateWithAI?: () => void;
}

// Categories for the tabs
const TEMPLATE_CATEGORIES = [
  { value: 'all', text: 'All' },
  { value: 'incident-management', text: 'Incident Management' },
  { value: 'kubernetes', text: 'Kubernetes' },
  { value: 'monitoring', text: 'Monitoring' },
  { value: 'deployment', text: 'Deployment' },
  { value: 'security', text: 'Security' },
  { value: 'cloud-cost', text: 'Cloud Cost' },
  { value: 'automation', text: 'Automation' },
];

// Category badge config: label, text color, border color, background
const CATEGORY_BADGE_CONFIG: { [key: string]: { label: string; color: string; borderColor: string; bg: string } } = {
  'incident-management': { label: 'Incident Management', color: '#EF4444', borderColor: '#ef444453', bg: 'rgba(239, 68, 68, 0.08)' },
  kubernetes: { label: 'Kubernetes', color: '#326CE5', borderColor: '#326ce553', bg: 'rgba(50, 108, 229, 0.08)' },
  monitoring: { label: 'Monitoring', color: '#F59E0B', borderColor: '#f59e0b53', bg: 'rgba(245, 158, 11, 0.08)' },
  deployment: { label: 'Deployment', color: '#10B981', borderColor: '#10b98153', bg: 'rgba(16, 185, 129, 0.08)' },
  security: { label: 'Security', color: '#8B5CF6', borderColor: '#8b5cf653', bg: 'rgba(139, 92, 246, 0.08)' },
  'cloud-cost': { label: 'Cloud Cost', color: '#EC4899', borderColor: '#ec489953', bg: 'rgba(236, 72, 153, 0.08)' },
  automation: { label: 'Automation', color: '#6366F1', borderColor: '#6366f153', bg: 'rgba(99, 102, 241, 0.08)' },
};

const DEFAULT_BADGE = { label: 'General', color: '#6B7280', borderColor: '#6b728053', bg: 'rgba(107, 114, 128, 0.08)' };

// Function to get appropriate icon based on task type (matches ActionNode.tsx)
const getTaskIcon = (taskType: string) => {
  if (!taskType) {
    return workflowSubWorkflowIcon?.default || workflowSubWorkflowIcon;
  }

  // First, check for specific task icons
  const specificTaskIcons: { [key: string]: any } = {
    'cloud.aws.cli': newAwsLogo,
    'cloud.azure.cli': ouAzure,
    'cloud.gcp.cli': ouGoogle,
    'cloud.k8s.cli': K8sIcon,
    'aws.cli': newAwsLogo,
    'azure.cli': ouAzure,
    'gcp.cli': ouGoogle,
    'k8s.cli': K8sIcon,
    'mq.rabbitmq.cli': RabbitmqIcon,
    'dbms.redis.cli': RedisLogoIcon,
    'scm.github.cli': GithubIcon,
    'cicd.argocd.cli': ArgocdIcon,
  };

  if (specificTaskIcons[taskType]) {
    return specificTaskIcons[taskType];
  }

  // Fall back to category-based icons
  const prefix = taskType.split('.')[0];
  const categoryMap: { [key: string]: any } = {
    cloud: CloudUploadIcon,
    dbms: workflowDatabaseIcon?.default || workflowDatabaseIcon,
    notifications: NotificationIcon1,
    observability: BarsBlueOutlineIcon,
    scripting: PlayCircleIcon,
    integrations: workflowWebhookIcon?.default || workflowWebhookIcon,
    tickets: TicketBlueIcon,
    llm: aiAgentIcon,
    data: workflowFormatterIcon?.default || workflowFormatterIcon,
    core: coreOpsIcon,
    cicd: IntegrationsIcon,
    network: workflowMessagingIcon?.default || workflowMessagingIcon,
    mq: workflowMessagingIcon?.default || workflowMessagingIcon,
    scm: GithubIcon,
    crypto: LLMFunctionIcon,
    events: BarsBlueOutlineIcon,
    aws: newAwsLogo,
    gcp: ouGoogle,
    azure: ouAzure,
    k8s: K8sIcon,
  };

  return categoryMap[prefix] || workflowSubWorkflowIcon?.default || workflowSubWorkflowIcon;
};

// Pastel gradient backgrounds for node icon circles
const NODE_ICON_GRADIENTS = [
  'linear-gradient(135deg, #FFEEF8 0%, #FFD6EC 100%)', // Pastel pink
  'linear-gradient(135deg, #F3E8FF 0%, #E9D5FF 100%)', // Pastel lavender
  'linear-gradient(135deg, #E0F2FE 0%, #BAE6FD 100%)', // Pastel sky blue
  'linear-gradient(135deg, #FEF3E2 0%, #FDEDD3 100%)', // Pastel peach
];

// Node icon component with circle background
const NodeIconCircle = ({ icon, index }: { icon: any; index: number }) => (
  <Box
    sx={{
      width: '28px',
      height: '28px',
      borderRadius: '50%',
      background: NODE_ICON_GRADIENTS[index % NODE_ICON_GRADIENTS.length],
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      marginLeft: index > 0 ? '-8px' : 0,
      zIndex: 10 - index,
      border: '2px solid white',
    }}
  >
    <SafeIcon src={icon} alt='node-icon' width={18} height={18} />
  </Box>
);

// Template card component
const TemplateCard = ({ workflow, onUseTemplate }: { workflow: any; onUseTemplate: (workflow: any) => void }) => {
  // Extract tasks from workflow definition
  const tasks = workflow?.definition?.tasks || [];
  const taskTypes = tasks.map((task: any) => task.type).filter(Boolean);
  const displayedIcons = taskTypes.slice(0, 4);
  const remainingCount = taskTypes.length > 4 ? taskTypes.length - 4 : 0;
  const badge = CATEGORY_BADGE_CONFIG[workflow.category] || DEFAULT_BADGE;

  return (
    <WidgetCard
      sx={{
        mt: 0,
        padding: '20px 24px',
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'space-between',
        boxShadow: '0px 4px 18px 10px rgba(229, 229, 229, 0.18), 0px 2px 8px 0px rgb(233, 233, 233)',
        minHeight: '100px',
        '&:hover': {
          transform: 'translateY(-2px)',
          boxShadow: '0px 4px 20px -1px rgba(229, 229, 229, 1.2 ), 0px 2px 20px 0px rgba(78, 78, 78, 0.24)',
          border: '1px solid #b394ff',
        },
        transition: 'all 0.2s ease',
      }}
    >
      <Box
        sx={{
          display: 'flex',

          flexDirection: 'column',
        }}
      >
        {/* Category Badge */}
        <Box
          sx={{
            display: 'inline-flex',
            alignItems: 'center',
            px: '8px',
            py: '4px',
            borderRadius: '50px',
            border: `1px solid ${badge.borderColor}`,
            backgroundColor: badge.bg,
            width: 'fit-content',
          }}
        >
          <Text
            value={badge.label}
            sx={{
              fontSize: '10px',
              fontWeight: 400,
              color: badge.color,
            }}
          />
        </Box>

        {/* Title */}
        <Text
          value={workflow.name || 'Untitled Automation'}
          sx={{
            fontSize: '13px',
            fontWeight: 600,
            fontFamily: 'Poppins',
            color: colors.text.secondary,
            mt: '12px',
            display: '-webkit-box',
            WebkitLineClamp: 2,
            WebkitBoxOrient: 'vertical',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            lineHeight: '1.3',
          }}
        />

        {/* Description */}
        <Text
          value={workflow.description || ''}
          sx={{
            fontSize: '12px',
            fontWeight: 400,
            color: colors.text.secondaryDark,
            mt: '8px',
            display: '-webkit-box',
            WebkitLineClamp: 2,
            WebkitBoxOrient: 'vertical',
            overflow: 'hidden',
            textOverflow: 'ellipsis',
            lineHeight: '1.4',
            flex: 1,
          }}
        />

        {/* Node Icons Row */}
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: '8px',
            mt: '16px',
          }}
        >
          <Box sx={{ display: 'flex', alignItems: 'center' }}>
            {displayedIcons.length > 0 ? (
              displayedIcons.map((taskType: string, index: number) => (
                <NodeIconCircle key={`${taskType}-${index}`} icon={getTaskIcon(taskType)} index={index} />
              ))
            ) : (
              // Show default icons if no tasks
              <>
                <NodeIconCircle icon={K8sIcon} index={0} />
                <NodeIconCircle icon={newAwsLogo} index={1} />
                <NodeIconCircle icon={GithubIcon} index={2} />
                <NodeIconCircle icon={NotificationIcon1} index={3} />
              </>
            )}
          </Box>
          {(remainingCount > 0 || displayedIcons.length === 0) && (
            <Text
              value={`+${remainingCount > 0 ? remainingCount : 3} more`}
              sx={{
                fontSize: '10px',
                fontWeight: 400,
                color: colors.text.secondaryDark,
              }}
            />
          )}
        </Box>
      </Box>

      {/* Use Template Button */}
      <Box sx={{ mt: '16px', width: '100%' }}>
        <CustomButton
          text='Use Template'
          variant='secondary'
          size='Medium'
          onClick={() => onUseTemplate(workflow)}
          sx={{
            width: '100%',
          }}
        />
      </Box>
    </WidgetCard>
  );
};

const WorkflowTemplatesModal: React.FC<WorkflowTemplatesModalProps> = ({
  open,
  onClose,
  accountId,
  eventSources,
  alertNames,
  subjectTypes,
  eventContext,
  onCreateWithAI,
}) => {
  const [selectedCategory, setSelectedCategory] = useState('all');
  const [workflows, setWorkflows] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const router = useRouter();

  const handleUseTemplate = useCallback(
    (workflow: any) => {
      // Store template data in sessionStorage (same pattern as AI-generated workflows)
      // Include eventContext so the builder can pre-fill input defaults
      const payload = eventContext ? { ...workflow, eventContext } : workflow;
      sessionStorage.setItem('templateWorkflow', JSON.stringify(payload));
      onClose();
      router.push(`/workflow/new?accountId=${accountId}&loadFromTemplate=true`);
    },
    [accountId, onClose, router, eventContext]
  );

  const handleCreateFromScratch = useCallback(() => {
    onClose();
    router.push(`/workflow/new?accountId=${accountId}`);
  }, [accountId, onClose, router]);

  // Fetch templates when modal opens
  const fetchWorkflows = useCallback(async () => {
    setLoading(true);
    try {
      const category = selectedCategory === 'all' ? undefined : selectedCategory;
      const response: any = await apiWorkflow.listTemplates(category, undefined, 50, undefined, eventSources, alertNames, subjectTypes);
      if (response?.data?.workflow_list_template?.templates) {
        setWorkflows(response.data.workflow_list_template.templates);
      } else {
        setWorkflows([]);
      }
    } catch (error) {
      console.error('Failed to fetch templates:', error);
    } finally {
      setLoading(false);
    }
  }, [selectedCategory, eventSources, alertNames, subjectTypes]);

  useEffect(() => {
    if (open) {
      fetchWorkflows();
    }
  }, [open, fetchWorkflows]);

  return (
    <Modal
      open={open}
      handleClose={onClose}
      width='lg'
      hideTitleBackground={true}
      title='Automate using pre-built templates'
      sx={{
        '& .MuiDialog-paper': {
          maxWidth: '1100px',
          maxHeight: '95vh',
        },
      }}
    >
      <Box
        sx={{
          padding: '0px',
          display: 'flex',
          flexDirection: 'column',
          maxHeight: 'calc(85vh - 80px)',
        }}
      >
        {/* Category Tabs */}
        <Box sx={{ mb: '8px' }}>
          <CustomTabs
            value={selectedCategory}
            onChange={setSelectedCategory}
            variant='primary'
            showBorderBottom={true}
            behavior='filter'
            options={{
              tabOptions: TEMPLATE_CATEGORIES,
            }}
          />
        </Box>

        {/* Cards Grid */}
        <Box
          sx={{
            overflowY: 'auto',
            flex: 1,
          }}
        >
          {loading ? (
            <Box
              sx={{
                display: 'flex',
                justifyContent: 'center',
                alignItems: 'center',
                height: '300px',
              }}
            >
              <Loader style={{ height: '100%', width: '100%' }} />
            </Box>
          ) : workflows.length === 0 ? (
            <Box
              sx={{
                display: 'flex',
                justifyContent: 'center',
                alignItems: 'center',
                height: '300px',
              }}
            >
              <Text value='No templates available' sx={{ color: colors.text.secondaryDark }} />
            </Box>
          ) : (
            <Box
              sx={{
                display: 'grid',
                gap: '20px',
                padding: '12px 20px',
                gridTemplateColumns: 'repeat(4, 1fr)',
                '@media (max-width: 1199px)': {
                  gridTemplateColumns: 'repeat(3, 1fr)',
                },
                '@media (max-width: 1023px)': {
                  gridTemplateColumns: 'repeat(2, 1fr)',
                },
                '@media (max-width: 767px)': {
                  gridTemplateColumns: '1fr',
                },
              }}
            >
              {workflows.map((workflow) => (
                <TemplateCard key={workflow.id} workflow={workflow} onUseTemplate={handleUseTemplate} />
              ))}
            </Box>
          )}
        </Box>

        {/* Bottom actions */}
        <Box sx={{ display: 'flex', justifyContent: 'center', gap: '12px', py: '16px', borderTop: '1px solid #eee' }}>
          {onCreateWithAI && <CustomButton text='Create with AI' variant='secondary' size='Medium' onClick={onCreateWithAI} />}
          <CustomButton text='Create from scratch' variant='tertiary' size='Medium' onClick={handleCreateFromScratch} />
        </Box>
      </Box>
    </Modal>
  );
};

export default WorkflowTemplatesModal;
