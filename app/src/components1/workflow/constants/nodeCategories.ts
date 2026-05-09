import {
  manualTriggerIcon,
  workflowMessagingIcon,
  workflowCalendarIcon,
  workflowFormatterIcon,
  workflowUserIcon,
  workflowDatabaseIcon,
  workflowWebhookIcon,
  aiAgentIcon,
  TicketBlueIcon,
  coreOpsIcon,
  CloudUploadIcon,
  BarsBlueOutlineIcon,
  NotificationIcon1,
  LLMFunctionIcon,
  PlayCircleIcon,
  IntegrationsIcon,
  RabbitmqIcon,
  RedisLogoIcon,
  GithubIcon,
  GitLabIcon,
  GitMergeIcon,
  ArgocdIcon,
  K8sIcon,
  newAwsLogo,
  ouAzure,
  ouGoogle,
  SlackIcon,
  McpIcon,
} from '@assets';
import { snakeToTitleCase } from 'src/utils/common';

import type { NodeCategories, TaskDefinition } from '@components1/workflow/types';

// Static trigger definitions
const staticTriggers = {
  triggers: {
    label: 'Triggers',
    icon: manualTriggerIcon?.default || manualTriggerIcon,
    description: 'Triggers are the starting points of an automation.',
    color: '#10b981', // Green
    subcategories: {
      manual: {
        label: 'Manual Trigger',
        description: 'Start automation manually',
        icon: workflowUserIcon?.default || workflowUserIcon,
        aliases: ['manual', 'button', 'start', 'run'],
      },
      webhook: {
        label: 'Webhook',
        description: 'HTTP endpoint trigger',
        icon: workflowWebhookIcon?.default || workflowWebhookIcon,
        aliases: ['webhook', 'http trigger', 'api trigger', 'incoming'],
      },
      schedule: {
        label: 'Schedule',
        description: 'Time-based trigger',
        icon: workflowCalendarIcon?.default || workflowCalendarIcon,
        aliases: ['schedule', 'cron', 'timer', 'scheduled', 'periodic', 'recurring'],
      },
      event: {
        label: 'Event Trigger',
        description: 'Event-based trigger',
        icon: '⚡',
        aliases: ['event', 'alert', 'alarm', 'incident trigger'],
      },
      optimization: {
        label: 'Optimization',
        description: 'Triggered by new recommendations',
        icon: '💡',
        aliases: ['optimization', 'recommendation', 'rightsize trigger'],
      },
    },
  },
};

// Function to get a color from a predefined palette for new categories
const getCategoryColor = (prefix: string): string => {
  const colors = [
    '#3b82f6', // Blue
    '#8b5cf6', // Purple
    '#f59e0b', // Amber
    '#06b6d4', // Cyan
    '#10b981', // Green
    '#f97316', // Orange
    '#ef4444', // Red
    '#ec4899', // Pink
    '#6366f1', // Indigo
    '#84cc16', // Lime
    '#f43f5e', // Rose
    '#0ea5e9', // Sky
    '#a855f7', // Violet
    '#059669', // Emerald
    '#dc2626', // Red
    '#7c3aed', // Violet
  ];

  // Use the prefix to deterministically pick a color
  const index = prefix.split('').reduce((acc, char) => acc + char.charCodeAt(0), 0) % colors.length;
  return colors[index];
};

// Function to get category icon and label based on task name prefix
const getCategoryInfo = (taskName: string) => {
  const prefix = taskName.split('.')[0];

  const categoryMap: { [key: string]: { label: string; icon: any; description: string; color: string } } = {
    cloud: { label: 'Cloud', icon: CloudUploadIcon, description: 'Run commands on AWS, Azure, GCP, or Kubernetes', color: '#3b82f6' }, // Blue
    dbms: {
      label: 'Database',
      icon: workflowDatabaseIcon?.default || workflowDatabaseIcon,
      description: 'Query databases and manage data stores',
      color: '#8b5cf6',
    }, // Purple
    notifications: {
      label: 'Notifications',
      icon: NotificationIcon1,
      description: 'Send messages via Slack, Teams, email, and more',
      color: '#f59e0b', // Amber
    },
    observability: {
      label: 'Observability',
      icon: BarsBlueOutlineIcon,
      description: 'Search logs, query metrics, and explore traces from your monitoring stack',
      color: '#06b6d4',
    }, // Cyan
    scripting: { label: 'Scripting', icon: PlayCircleIcon, description: 'Run custom shell scripts and automation code', color: '#10b981' }, // Green
    integrations: {
      label: 'Integrations',
      icon: workflowWebhookIcon?.default || workflowWebhookIcon,
      description: 'Connect to external services via HTTP or SSH',
      color: '#f97316', // Orange
    },
    tickets: { label: 'Tickets', icon: TicketBlueIcon, description: 'Create, update, and manage incidents and tickets', color: '#3b82f6' }, // Blue
    llm: { label: 'AI/LLM', icon: aiAgentIcon, description: 'Use AI to summarize, investigate, and route decisions', color: '#a855f7' }, // Purple
    data: {
      label: 'Data',
      icon: workflowFormatterIcon?.default || workflowFormatterIcon,
      description: 'Transform, filter, and reshape data between steps',
      color: '#FFCC00',
    }, // Teal
    core: { label: 'Core', icon: coreOpsIcon, description: 'Control flow: loops, branches, approvals, and sub-automations', color: '#6366f1' }, // Indigo
    cicd: { label: 'CI/CD', icon: IntegrationsIcon, description: 'Manage deployments with ArgoCD and other CI/CD tools', color: '#8b5cf6' }, // Purple
    network: {
      label: 'Networking',
      icon: workflowMessagingIcon?.default || workflowMessagingIcon,
      description: 'Diagnose connectivity with DNS, ping, SSL checks, and more',
      color: '#f43f5e',
    },
    mq: {
      label: 'Message Queue',
      icon: workflowMessagingIcon?.default || workflowMessagingIcon,
      description: 'Manage RabbitMQ queues and message routing',
      color: '#ec4899',
    }, // Pink
    scm: { label: 'Source Control', icon: GitMergeIcon, description: 'Manage GitHub repos, issues, and pull requests', color: '#6366f1' }, // Indigo
    crypto: {
      label: 'Cryptography',
      icon: LLMFunctionIcon,
      description: 'Encode, decode, hash, encrypt, and decrypt data',
      color: '#7c3aed',
    }, // Violet
    events: {
      label: 'Events',
      icon: BarsBlueOutlineIcon,
      description: 'Store events for troubleshooting and audit trails',
      color: '#059669',
    }, // Emerald
    aws: { label: 'AWS', icon: newAwsLogo, description: 'Run AWS CLI commands against your AWS account', color: '#ff9900' }, // AWS Orange
    gcp: { label: 'GCP', icon: ouGoogle, description: 'Run gcloud commands against your GCP project', color: '#4285f4' }, // Google Blue
    azure: { label: 'Azure', icon: ouAzure, description: 'Run Azure CLI commands against your Azure subscription', color: '#0078d4' }, // Azure Blue
    k8s: { label: 'Kubernetes', icon: K8sIcon, description: 'Run kubectl commands against your Kubernetes cluster', color: '#326ce5' }, // Kubernetes Blue
    slack: { label: 'Slack', icon: SlackIcon, description: 'Slack messaging and channel management', color: '#4A154B' }, // Slack Purple
    mcp: { label: 'MCP', icon: McpIcon, description: 'Execute tools via Model Context Protocol servers', color: '#1a1a1a' },
  };

  // If category is found in predefined map, return it
  if (categoryMap[prefix]) {
    return categoryMap[prefix];
  }

  // Dynamically create new category for unknown prefixes. Use snakeToTitleCase regardless
  // of underscore presence so single-word acronym prefixes (e.g. "iam", "mcp") render as
  // "IAM"/"MCP" via the shared UPPERCASE_ACRONYMS set, and multi-word snake_case prefixes
  // (e.g. "vertical_rightsize_generate") render as "Vertical Rightsize Generate".
  const humanPrefix = snakeToTitleCase(prefix);
  return {
    label: humanPrefix,
    icon: PlayCircleIcon, // Generic tool icon for new categories
    description: `${humanPrefix} operations and integrations`,
    color: getCategoryColor(prefix),
  };
};

// Function to get specific task icon based on full task name
const getSpecificTaskIcon = (taskName: string) => {
  const taskIconMap: { [key: string]: any } = {
    // Cloud providers
    'cloud.aws.cli': newAwsLogo,
    'cloud.azure.cli': ouAzure,
    'cloud.gcp.cli': ouGoogle,
    'cloud.k8s.cli': K8sIcon,

    // Message queues
    'mq.rabbitmqadmin.cli': RabbitmqIcon,

    // Databases
    'dbms.redis.cli': RedisLogoIcon,

    // Source control
    'scm.github.cli': GithubIcon,
    'scm.gitlab.cli': GitLabIcon,

    // CI/CD
    'cicd.argocd.cli': ArgocdIcon,

    // MCP
    'llm.mcp_call': McpIcon,
  };

  return taskIconMap[taskName] || null;
};

// Function to create a readable label from task name parts
export const createTaskLabel = (taskName: string): string => {
  const parts = taskName.split('.');
  // Skip the first part (category) and process the remaining parts. If the task name has
  // no dot (e.g. `vertical_rightsize_generate`), use the whole name so the label isn't empty.
  const taskParts = parts.length > 1 ? parts.slice(1) : parts;

  // Known acronyms that should be uppercase
  const acronyms = new Set([
    'cli',
    'api',
    'aws',
    'gcp',
    'cpu',
    'ram',
    'url',
    'http',
    'https',
    'ssh',
    'ftp',
    'dns',
    'ntp',
    'tcp',
    'udp',
    'ip',
    'sql',
    'json',
    'xml',
    'yaml',
    'html',
    'css',
    'js',
    'ts',
    'ui',
    'ux',
    'db',
    'vm',
    'os',
    'ai',
    'ml',
    'llm',
    'gpu',
    'ssd',
    'hdd',
    'raid',
    'vpn',
    'cdn',
    'ssl',
    'tls',
    'jwt',
    'oauth',
    'saml',
    'ldap',
    'smtp',
    'imap',
    'pop',
    'rpc',
    'rest',
    'soap',
    'grpc',
    'mqtt',
    'amqp',
    'kafka',
    'redis',
    'nginx',
    'apache',
    'docker',
    'k8s',
    'kubernetes',
    'helm',
    'terraform',
    'ansible',
    'jenkins',
    'gitlab',
    'github',
    'jira',
    'slack',
    'teams',
    'zoom',
    'aws',
    'azure',
    'gcp',
    'iam',
    'rbac',
    'acl',
    'cicd',
    'devops',
    'sre',
    'ops',
    'prod',
    'dev',
    'qa',
    'uat',
    'staging',
    'cors',
    'csrf',
    'xss',
    'ddos',
    'dos',
  ]);

  return taskParts
    .map((part) => {
      // Split by underscore to handle snake_case
      const words = part.split('_');

      return words
        .map((word) => {
          const lowerWord = word.toLowerCase();
          if (acronyms.has(lowerWord)) {
            return lowerWord.toUpperCase();
          }
          // Regular word capitalization
          return word.charAt(0).toUpperCase() + word.slice(1).toLowerCase();
        })
        .join(' ');
    })
    .join(' ');
};

// Client-side deprecation list. Keep in sync with ActionNode's DEPRECATED_TASK_TYPES.
// Will be removed once the backend exposes a deprecated flag on TaskDefinition.
const DEPRECATED_TASKS: Record<string, string> = {
  'llm.router': 'llm.router is deprecated. Use llm.classify + core.switch directly for the same behavior.',
};

// Function to generate node categories from task definitions
export const generateNodeCategories = (taskDefinitions: TaskDefinition[]): NodeCategories => {
  const categories: NodeCategories = { ...staticTriggers };

  // Deduplicate: when both cloud.X.cli and X.cli exist, keep only X.cli
  // (cloud.* are legacy aliases of the direct provider tasks)
  const taskNames = new Set(taskDefinitions.map((t) => t.name));
  const filteredTasks = taskDefinitions.filter((task) => {
    if (task.name.startsWith('cloud.')) {
      const directName = task.name.replace('cloud.', '');
      if (taskNames.has(directName)) return false;
    }
    return true;
  });

  // Group tasks by category prefix
  const categoryGroups: { [key: string]: TaskDefinition[] } = {};

  filteredTasks.forEach((task) => {
    const prefix = task.name.split('.')[0];
    if (!categoryGroups[prefix]) {
      categoryGroups[prefix] = [];
    }
    categoryGroups[prefix].push(task);
  });

  // Convert each category group to node category format
  Object.entries(categoryGroups).forEach(([prefix, tasks]) => {
    const categoryInfo = getCategoryInfo(prefix);

    const subcategories: {
      [key: string]: {
        completeLabel: string;
        label: string;
        description: string;
        icon: string;
        aliases?: string[];
        deprecated?: boolean;
        deprecationMessage?: string;
      };
    } = {};

    const sortedTasks = tasks.slice().sort((a, b) => Number(a.name in DEPRECATED_TASKS) - Number(b.name in DEPRECATED_TASKS));

    sortedTasks.forEach((task) => {
      // Use the full task name as the key for subcategory
      const taskKey = task.name;
      // Create a readable label: use display_name if available, otherwise generate from task name.
      // When display_name comes back in snake_case (e.g. "vertical_rightsize_generate"), normalize
      // it to Title Case so the picker doesn't show raw identifiers.
      const label = task.display_name
        ? task.display_name.includes('_')
          ? snakeToTitleCase(task.display_name)
          : task.display_name
        : createTaskLabel(task.name);
      const fullLabel = `${categoryInfo.label} - ${label}`;
      // Get specific icon for this task, or fall back to category icon
      const specificIcon = getSpecificTaskIcon(task.name);
      const taskIcon = specificIcon || categoryInfo.icon;

      const deprecationMessage = DEPRECATED_TASKS[task.name];

      subcategories[taskKey] = {
        completeLabel: fullLabel,
        label: label,
        description: task.description,
        icon: taskIcon,
        aliases: task.aliases,
        deprecated: deprecationMessage !== undefined,
        deprecationMessage,
      };
    });

    categories[prefix] = {
      label: categoryInfo.label,
      icon: categoryInfo.icon,
      description: categoryInfo.description,
      color: categoryInfo.color,
      subcategories: subcategories,
    };
  });

  return categories;
};

// Default static categories (fallback)
export const nodeCategories: NodeCategories = staticTriggers;
