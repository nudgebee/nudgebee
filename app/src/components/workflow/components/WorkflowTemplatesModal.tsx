import React, { useEffect, useState, useCallback } from 'react';
import { Box } from '@mui/material';
import { Modal } from '@ui/Modal';
import Text from '@shared/format/Text';
import WidgetCard from '@ui/WidgetCard';
import Tabs from '@shared/navigation/Tabs';
import apiWorkflow from '@api1/workflow';
import { useRouter } from 'next/router';
import { ds } from 'src/utils/colors';
import SafeIcon from '@shared/icons/SafeIcon';
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
import { Button } from '@ui/Button';
import Loader from '@shared/Loader';

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
  'incident-management': {
    label: 'Incident Management',
    color: 'var(--ds-red-500)',
    borderColor: 'color-mix(in srgb, var(--ds-red-500) 33%, transparent)',
    bg: 'color-mix(in srgb, var(--ds-red-500) 8%, transparent)',
  },
  kubernetes: {
    label: 'Kubernetes',
    color: 'var(--ds-blue-500)',
    borderColor: 'color-mix(in srgb, var(--ds-blue-500) 33%, transparent)',
    bg: 'color-mix(in srgb, var(--ds-blue-500) 8%, transparent)',
  },
  monitoring: {
    label: 'Monitoring',
    color: 'var(--ds-amber-400)',
    borderColor: 'color-mix(in srgb, var(--ds-amber-400) 33%, transparent)',
    bg: 'color-mix(in srgb, var(--ds-amber-400) 8%, transparent)',
  },
  deployment: {
    label: 'Deployment',
    color: 'var(--ds-teal-500)',
    borderColor: 'color-mix(in srgb, var(--ds-teal-500) 33%, transparent)',
    bg: 'color-mix(in srgb, var(--ds-teal-500) 8%, transparent)',
  },
  security: {
    label: 'Security',
    color: 'var(--ds-purple-400)',
    borderColor: 'color-mix(in srgb, var(--ds-purple-400) 33%, transparent)',
    bg: 'color-mix(in srgb, var(--ds-purple-400) 8%, transparent)',
  },
  'cloud-cost': {
    label: 'Cloud Cost',
    color: 'var(--ds-pink-400)',
    borderColor: 'color-mix(in srgb, var(--ds-pink-400) 33%, transparent)',
    bg: 'color-mix(in srgb, var(--ds-pink-400) 8%, transparent)',
  },
  automation: {
    label: 'Automation',
    color: 'var(--ds-purple-400)',
    borderColor: 'color-mix(in srgb, var(--ds-purple-400) 33%, transparent)',
    bg: 'color-mix(in srgb, var(--ds-purple-400) 8%, transparent)',
  },
};

const DEFAULT_BADGE = {
  label: 'General',
  color: 'var(--ds-gray-600)',
  borderColor: 'color-mix(in srgb, var(--ds-gray-600) 33%, transparent)',
  bg: 'color-mix(in srgb, var(--ds-gray-600) 8%, transparent)',
};

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
  'linear-gradient(135deg, var(--ds-pink-100) 0%, var(--ds-pink-200) 100%)',
  'linear-gradient(135deg, var(--ds-purple-100) 0%, var(--ds-purple-200) 100%)',
  'linear-gradient(135deg, var(--ds-blue-100) 0%, var(--ds-blue-300) 100%)',
  'linear-gradient(135deg, var(--ds-amber-100) 0%, var(--ds-amber-200) 100%)',
];

// Node icon component with circle background
const NodeIconCircle = ({ icon, index }: { icon: any; index: number }) => (
  <Box
    sx={{
      width: ds.space.mul(1, 7),
      height: ds.space.mul(1, 7),
      borderRadius: '50%',
      background: NODE_ICON_GRADIENTS[index % NODE_ICON_GRADIENTS.length],
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      marginLeft: index > 0 ? ds.space.mul(2, -1) : 0,
      zIndex: 10 - index,
      border: `${ds.space[0]} solid ${ds.background[100]}`,
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
        padding: 'var(--ds-space-4) var(--ds-space-5)',
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'space-between',
        boxShadow: `0 ${ds.space[1]} ${ds.space.mul(0, 9)} ${ds.space.mul(0, 5)} rgba(229, 229, 229, 0.18), 0 ${ds.space[0]} ${
          ds.space[2]
        } 0 rgb(233, 233, 233)`,
        minHeight: ds.space.mul(0, 50),
        '&:hover': {
          transform: `translateY(${ds.space.mul(0, -1)})`,
          boxShadow: `0 ${ds.space[1]} ${ds.space.mul(0, 10)} -1px rgba(229, 229, 229, 1.2), 0 ${ds.space[0]} ${ds.space.mul(
            0,
            10
          )} 0 rgba(78, 78, 78, 0.24)`,
          border: '1px solid var(--ds-purple-300)',
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
            px: 'var(--ds-space-2)',
            py: 'var(--ds-space-1)',
            borderRadius: 'var(--ds-radius-xl)',
            border: `1px solid ${badge.borderColor}`,
            backgroundColor: badge.bg,
            width: 'fit-content',
          }}
        >
          <Text
            value={badge.label}
            sx={{
              fontSize: 'var(--ds-text-caption)',
              fontWeight: 'var(--ds-font-weight-regular)',
              color: badge.color,
            }}
          />
        </Box>

        {/* Title */}
        <Text
          value={workflow.name || 'Untitled Automation'}
          sx={{
            fontSize: 'var(--ds-text-body)',
            fontWeight: 'var(--ds-font-weight-semibold)',
            fontFamily: 'Poppins',
            color: ds.gray[700],
            mt: 'var(--ds-space-3)',
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
            fontSize: 'var(--ds-text-small)',
            fontWeight: 'var(--ds-font-weight-regular)',
            color: ds.gray[400],
            mt: 'var(--ds-space-2)',
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
            gap: 'var(--ds-space-2)',
            mt: 'var(--ds-space-4)',
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
                fontSize: 'var(--ds-text-caption)',
                fontWeight: 'var(--ds-font-weight-regular)',
                color: ds.gray[400],
              }}
            />
          )}
        </Box>
      </Box>

      {/* Use Template Button */}
      <Box sx={{ mt: 'var(--ds-space-4)', width: '100%' }}>
        <Button tone='secondary' size='md' fullWidth onClick={() => onUseTemplate(workflow)}>
          Use Template
        </Button>
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
          maxWidth: ds.space.mul(0, 550),
          maxHeight: '95vh',
        },
      }}
    >
      <Box
        sx={{
          padding: '0px',
          display: 'flex',
          flexDirection: 'column',
          maxHeight: `calc(85vh - ${ds.space.mul(2, 10)})`,
        }}
      >
        {/* Category Tabs */}
        <Box sx={{ mb: 'var(--ds-space-2)' }}>
          <Tabs
            value={selectedCategory}
            onChange={setSelectedCategory}
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
                height: ds.space.mul(3, 25),
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
                height: ds.space.mul(3, 25),
              }}
            >
              <Text value='No templates available' sx={{ color: ds.gray[400] }} />
            </Box>
          ) : (
            <Box
              sx={{
                display: 'grid',
                gap: 'var(--ds-space-4)',
                padding: 'var(--ds-space-3) var(--ds-space-4)',
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
        <Box
          sx={{
            display: 'flex',
            justifyContent: 'flex-end',
            gap: 'var(--ds-space-3)',
            paddingTop: 'var(--ds-space-4)',
            borderTop: '1px solid var(--ds-gray-200)',
          }}
        >
          {onCreateWithAI && (
            <Button tone='secondary' size='md' onClick={onCreateWithAI}>
              Create with AI
            </Button>
          )}
          <Button tone='secondary' size='md' onClick={handleCreateFromScratch}>
            Create from scratch
          </Button>
        </Box>
      </Box>
    </Modal>
  );
};

export default WorkflowTemplatesModal;
