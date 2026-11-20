// DORA delivery metrics for Troubleshoot > Analytics (#300).
//
// The counting (what a deploy, a failed deploy and a recovery are) happens in
// the backend columns doraAggregates reads. This file only turns those counts
// into the numbers and DORA performance levels the tiles show, so the
// thresholds can be tested without rendering anything.

export type DoraLevel = 'Elite' | 'High' | 'Medium' | 'Low';

/**
 * Below this many deploys or recoveries a rate or median is not a measurement,
 * so the level is withheld. Same floor the "Time to explain a problem" tile uses.
 */
export const DORA_MIN_SAMPLES = 5;

const DAY_MINUTES = 24 * 60;
const WEEK_MINUTES = 7 * DAY_MINUTES;

/** Deploys per workload per day: on demand (daily or more), weekly, monthly, rarer. */
export const classifyDeploymentFrequency = (perWorkloadPerDay: number): DoraLevel => {
  if (perWorkloadPerDay >= 1) return 'Elite';
  if (perWorkloadPerDay >= 1 / 7) return 'High';
  if (perWorkloadPerDay >= 1 / 30) return 'Medium';
  return 'Low';
};

/** Share of deploys (0–1) followed by an urgent issue they likely caused. */
export const classifyChangeFailureRate = (rate: number): DoraLevel => {
  if (rate <= 0.05) return 'Elite';
  if (rate <= 0.1) return 'High';
  if (rate <= 0.15) return 'Medium';
  return 'Low';
};

/** Median minutes to recover: under an hour, a day, a week, longer. */
export const classifyRecoveryMinutes = (minutes: number): DoraLevel => {
  if (minutes < 60) return 'Elite';
  if (minutes < DAY_MINUTES) return 'High';
  if (minutes < WEEK_MINUTES) return 'Medium';
  return 'Low';
};

export interface DoraInput {
  deploys: number;
  failedChanges: number;
  deployedWorkloads: number;
  resolvedIssues: number;
  medianRecoveryMinutes: number;
  windowDays: number;
}

export interface DoraModel {
  deploymentFrequency: { perDay: number; perWorkloadPerDay: number; level: DoraLevel | null };
  changeFailureRate: { rate: number | null; level: DoraLevel | null };
  recovery: { minutes: number | null; level: DoraLevel | null };
}

const finite = (value: number): number => (Number.isFinite(value) && value > 0 ? value : 0);

export const deriveDora = (input: DoraInput): DoraModel => {
  const deploys = finite(input.deploys);
  const failed = Math.min(finite(input.failedChanges), deploys);
  const workloads = finite(input.deployedWorkloads);
  const resolved = finite(input.resolvedIssues);
  const recoveryMinutes = finite(input.medianRecoveryMinutes);
  const days = finite(input.windowDays);

  const perDay = days > 0 ? deploys / days : 0;
  // Classified per workload: DORA rates a service's cadence, and a tenant-wide
  // total would call 200 services that each deploy monthly "Elite".
  const perWorkloadPerDay = workloads > 0 ? perDay / workloads : 0;
  const enoughDeploys = deploys >= DORA_MIN_SAMPLES;
  const rate = deploys > 0 ? failed / deploys : null;

  return {
    deploymentFrequency: {
      perDay,
      perWorkloadPerDay,
      level: enoughDeploys && workloads > 0 && days > 0 ? classifyDeploymentFrequency(perWorkloadPerDay) : null,
    },
    changeFailureRate: { rate, level: enoughDeploys && rate !== null ? classifyChangeFailureRate(rate) : null },
    recovery: {
      minutes: resolved > 0 && recoveryMinutes > 0 ? recoveryMinutes : null,
      level: resolved >= DORA_MIN_SAMPLES && recoveryMinutes > 0 ? classifyRecoveryMinutes(recoveryMinutes) : null,
    },
  };
};

/** "3.2 per day" when daily or more, otherwise "1.5 per week", so small rates stay readable. */
export const formatDeployRate = (perDay: number): string => {
  if (perDay >= 1) return `${perDay.toFixed(1)} per day`;
  return `${(perDay * 7).toFixed(1)} per week`;
};

export const formatRecovery = (minutes: number): string => {
  if (minutes < 60) return `${Math.round(minutes)} min`;
  if (minutes < DAY_MINUTES) return `${(minutes / 60).toFixed(1)} hrs`;
  return `${(minutes / DAY_MINUTES).toFixed(1)} days`;
};
