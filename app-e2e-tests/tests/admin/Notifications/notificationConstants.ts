// Non-null assertions (!) are intentional: these are E2E test fixtures loaded from .env files,
// not production inputs. A missing value will cause the test to fail with a clear "undefined" error,
// which is the desired behavior. Regex validation would add noise without improving signal here.
export const slack = {
  slack_troubleshoot: process.env.SLACK_TROUBLESHOOT_CHANNEL!,
  slack_optimization: process.env.SLACK_OPTIMIZATION_CHANNEL!,
  slack_autopilot: process.env.SLACK_AUTOPILOT_CHANNEL!,
  slack_slo: process.env.SLACK_SLO_CHANNEL!,
  slack_daily_high: process.env.SLACK_DAILY_HIGH_CHANNEL!,
};

export const msteams = {
  msteams_group_name: process.env.MSTEAMS_GROUP_NAME!,
  msteams_troubleshoot: process.env.MSTEAMS_TROUBLESHOOT_CHANNEL!,
  msteams_optimization: process.env.MSTEAMS_OPTIMIZATION_CHANNEL!,
  msteams_autopilot: process.env.MSTEAMS_AUTOPILOT_CHANNEL!,
  msteams_slo: process.env.MSTEAMS_SLO_CHANNEL!,
  msteams_daily_high: process.env.MSTEAMS_DAILY_HIGH_CHANNEL!,
};

export const gchat = {
  gchat_troubleshoot: process.env.GCHAT_TROUBLESHOOT_CHANNEL!,
  gchat_optimization: process.env.GCHAT_OPTIMIZATION_CHANNEL!,
  gchat_autopilot: process.env.GCHAT_AUTOPILOT_CHANNEL!,
  gchat_slo: process.env.GCHAT_SLO_CHANNEL!,
  gchat_daily_high: process.env.GCHAT_DAILY_HIGH_CHANNEL!,
};

export const ruleNames = {
  rule_1: process.env.RULE_NAME_TROUBLESHOOT!,
  rule_2: process.env.RULE_NAME_OPTIMIZATION!,
  rule_3: process.env.RULE_NAME_AUTOPILOT!,
  rule_4: process.env.RULE_NAME_SLO!,
  rule_5: process.env.RULE_NAME_DAILY_HIGH!,
};
