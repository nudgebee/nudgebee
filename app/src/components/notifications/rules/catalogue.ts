import { troubleshootIconBlack, OptimizeIconBlack, SLOInspectionBlackIcon, CloudIconBlackOutline, AutomateBlue, EmailIconBlack } from '@assets';

export type RuleTypeKey = 'troubleshoot' | 'slo' | 'cloud' | 'optimize' | 'automation' | 'weekly_digest' | 'daily_recap' | 'llm_budget';

export interface RuleEvent {
  id: string;
  label: string;
  description: string;
}

export interface RuleType {
  key: RuleTypeKey;
  label: string;
  // Plural and singular nouns for sentences ("troubleshooting alerts").
  noun: string;
  one: string;
  // Can be limited to accounts; the others always cover every account.
  accounts: boolean;
  // Namespace and application filters apply.
  workloads: boolean;
  // Alert, severity and hourly grouping apply (a rule of this type alone).
  alerts: boolean;
  // Covered by a rule for all types. The daily email is its own singleton rule.
  inAllTypes: boolean;
  // A rule can only cover this type on its own.
  alone: boolean;
  icon: any;
  includes: string[];
  events?: RuleEvent[];
  defaultEvents?: string[];
  // Messages of this type that cover several accounts, each split per channel.
  rollup?: string;
}

// The Auto-Optimize and workflow messages a rule can select. Approval requests
// are never stopped, so no rule selects them.
export const AUTOMATION_EVENTS: RuleEvent[] = [
  { id: 'automation.task.completed', label: 'Run completed', description: 'Auto-Optimize run summary: applied, tickets, skipped' },
  { id: 'automation.task.progress', label: 'Workflow messages', description: 'Messages a workflow’s notification step posts' },
  { id: 'automation.pr.created', label: 'Pull requests created', description: 'Follow-up once a run’s pull requests are open' },
  { id: 'automation.task.failed', label: 'Run failed', description: 'An Auto-Optimize run had a failed task' },
  { id: 'automation.pr.failed', label: 'Pull requests failed', description: 'A run’s pull requests could not be created' },
];
export const APPROVAL_EVENT = 'automation.approval.requested';

export const OPTIMIZE_EVENTS: RuleEvent[] = [
  { id: 'optimize.recommendation.applied', label: 'Recommendation applied', description: 'A recommendation was applied (one account)' },
  { id: 'optimize.alert.fixed', label: 'Alert fixed', description: 'An alert was fixed by applying its resolution (one account)' },
  { id: 'optimize.digest', label: 'Daily recommendation digest', description: '08:00 every day, several accounts' },
  { id: 'optimize.nudge', label: 'Proactive nudges', description: 'Every 6 hours, several accounts' },
  { id: 'optimize.security_posture', label: 'Security posture alert', description: '08:30 every day, several accounts' },
];

export const RULE_TYPES: RuleType[] = [
  {
    key: 'troubleshoot',
    label: 'Troubleshooting',
    noun: 'troubleshooting alerts',
    one: 'troubleshooting alert',
    accounts: true,
    workloads: true,
    alerts: true,
    inAllTypes: true,
    alone: false,
    icon: troubleshootIconBlack,
    includes: [
      'New alerts from Prometheus, Datadog, CloudWatch, GCP, New Relic and every other connected source, including cloud alarms and anomaly alerts. Only High alerts are sent right away.',
    ],
  },
  {
    key: 'slo',
    label: 'SLO',
    noun: 'SLO alerts',
    one: 'SLO alert',
    accounts: true,
    workloads: true,
    alerts: false,
    inAllTypes: true,
    alone: false,
    icon: SLOInspectionBlackIcon,
    includes: ['SLO burn-rate breaches, checked every hour and grouped into one message per check.'],
  },
  {
    key: 'cloud',
    label: 'Cloud spend',
    noun: 'cloud spend summaries',
    one: 'cloud spend summary',
    accounts: true,
    workloads: false,
    alerts: false,
    inAllTypes: true,
    alone: false,
    icon: CloudIconBlackOutline,
    includes: ['The daily cloud spend summary for each account. Cloud alarms (CloudWatch, GCP alerts) are under Troubleshooting.'],
  },
  {
    key: 'optimize',
    label: 'Optimization',
    noun: 'optimization updates',
    one: 'optimization update',
    accounts: true,
    workloads: false,
    alerts: false,
    inAllTypes: true,
    alone: false,
    icon: OptimizeIconBlack,
    includes: [
      'A recommendation was applied, or an alert was fixed by applying its resolution (one account each).',
      'The daily recommendation digest, proactive nudges and the daily security posture alert (several accounts each).',
    ],
    events: OPTIMIZE_EVENTS,
    defaultEvents: OPTIMIZE_EVENTS.map((e) => e.id),
    rollup: 'recommendation digests, proactive nudges and security posture alerts',
  },
  {
    key: 'automation',
    label: 'Automation',
    noun: 'automation updates',
    one: 'automation update',
    accounts: true,
    workloads: false,
    alerts: false,
    inAllTypes: true,
    alone: false,
    icon: AutomateBlue,
    includes: [
      'Auto-Optimize run results, pull request follow-ups, workflow message steps and approval requests.',
      'Workflow email and direct-message steps are sent as the workflow says; rules don’t apply to them.',
    ],
    events: AUTOMATION_EVENTS,
    defaultEvents: ['automation.task.completed', 'automation.task.progress', 'automation.pr.created'],
  },
  {
    key: 'weekly_digest',
    label: 'Weekly digest',
    noun: 'weekly digests',
    one: 'weekly digest',
    accounts: false,
    workloads: false,
    alerts: false,
    inAllTypes: true,
    alone: false,
    icon: EmailIconBlack,
    includes: ['The weekly event-analysis digest. It always covers every account: its summary and PDF can’t be split by account.'],
  },
  {
    key: 'daily_recap',
    label: 'Daily email',
    noun: 'daily emails',
    one: 'daily email',
    accounts: false,
    workloads: false,
    alerts: false,
    inAllTypes: false,
    alone: true,
    icon: EmailIconBlack,
    includes: ['The daily highlights email to your users. There is one daily email rule.'],
  },
  {
    key: 'llm_budget',
    label: 'AI Gateway budget alerts',
    noun: 'AI Gateway budget alerts',
    one: 'AI Gateway budget alert',
    accounts: false,
    workloads: false,
    alerts: false,
    inAllTypes: false,
    alone: true,
    icon: OptimizeIconBlack,
    includes: [
      'A message when an AI Gateway quota with a warn level passes it, and again when it reaches its limit, once each per window.',
      'Sent to a channel only when a rule for this type exists. The key owner is always emailed directly.',
    ],
  },
];

export const RULE_TYPE: Record<RuleTypeKey, RuleType> = Object.fromEntries(RULE_TYPES.map((t) => [t.key, t])) as Record<RuleTypeKey, RuleType>;
export const ALL_TYPE_KEYS: RuleTypeKey[] = RULE_TYPES.filter((t) => t.inAllTypes).map((t) => t.key);

// Types with their own "When no rule matches" setting, after the one for all types.
export const FALLBACK_TYPE_KEYS: RuleTypeKey[] = ALL_TYPE_KEYS;

export const isRuleTypeKey = (value: unknown): value is RuleTypeKey => typeof value === 'string' && value in RULE_TYPE;
