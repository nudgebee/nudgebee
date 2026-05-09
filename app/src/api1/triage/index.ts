import { queryGraphQL } from '@lib/HttpService';

// -------------------- GraphQL Mutations --------------------

export const EVENT_CLASSIFY = `
mutation EventClassify(
  $event_id: String!
  $classification: String!
  $reason_code: String!
  $reason_text: String
  $priority_direction: String
  $corrected_priority: String
  $apply_scope: String
  $apply_until_hours: Int
  $linked_event_id: String
  $apply_to_existing: Boolean
  $confirmed: Boolean
) {
  event_classify(
    event_id: $event_id
    classification: $classification
    reason_code: $reason_code
    reason_text: $reason_text
    priority_direction: $priority_direction
    corrected_priority: $corrected_priority
    apply_scope: $apply_scope
    apply_until_hours: $apply_until_hours
    linked_event_id: $linked_event_id
    apply_to_existing: $apply_to_existing
    confirmed: $confirmed
  ) {
    success
    classification_id
    rule_created
    rule_id
    rule_expires_at
    bulk_operation {
      job_id
      events_to_update
      status
    }
  }
}
`;

export const EVENT_CLASSIFY_PREVIEW = `
mutation EventClassifyPreview(
  $event_id: String!
  $classification: String!
  $apply_scope: String
  $apply_until_hours: Int
) {
  event_classify_preview(
    event_id: $event_id
    classification: $classification
    apply_scope: $apply_scope
    apply_until_hours: $apply_until_hours
  ) {
    current_event {
      id
      title
      new_status
    }
    existing_events {
      count
      sample_titles
      will_be_updated
    }
    future_events {
      rule_applies
      scope_description
    }
    rule_to_create {
      rule_type
      match_criteria
      action
      expires_at
    }
  }
}
`;

export const EVENT_CREATE_TRIAGE_RULE = `
mutation EventCreateTriageRule(
  $cloud_account_id: String!
  $rule_type: String!
  $action: String!
  $match_source: String
  $match_alertname: String
  $match_namespace: String
  $match_service: String
  $match_fingerprint: String
  $match_labels: String
  $match_priority: String
  $match_finding_type: String
  $action_value: String
  $priority: Int
  $effective_until: String
  $name: String
  $description: String
  $apply_to_existing: Boolean
) {
  event_create_triage_rule(
    cloud_account_id: $cloud_account_id
    rule_type: $rule_type
    action: $action
    match_source: $match_source
    match_alertname: $match_alertname
    match_namespace: $match_namespace
    match_service: $match_service
    match_fingerprint: $match_fingerprint
    match_labels: $match_labels
    match_priority: $match_priority
    match_finding_type: $match_finding_type
    action_value: $action_value
    priority: $priority
    effective_until: $effective_until
    name: $name
    description: $description
    apply_to_existing: $apply_to_existing
  ) {
    success
    rule {
      id
      name
      rule_type
      action
      priority
      enabled
      match_alertname
      match_namespace
      match_service
      match_fingerprint
      match_source
      match_priority
      match_labels
      action_value
      effective_from
      effective_until
      created_at
      updated_at
    }
    bulk_operation {
      events_to_update
      status
    }
    error
  }
}
`;

export const EVENT_UPDATE_TRIAGE_RULE = `
mutation EventUpdateTriageRule(
  $cloud_account_id: String!
  $rule_id: String!
  $rule_type: String!
  $action: String!
  $match_source: String
  $match_alertname: String
  $match_namespace: String
  $match_service: String
  $match_fingerprint: String
  $match_labels: String
  $match_priority: String
  $match_finding_type: String
  $action_value: String
  $priority: Int
  $effective_until: String
  $name: String
  $description: String
  $apply_to_existing: Boolean
) {
  event_update_triage_rule(
    cloud_account_id: $cloud_account_id
    rule_id: $rule_id
    rule_type: $rule_type
    action: $action
    match_source: $match_source
    match_alertname: $match_alertname
    match_namespace: $match_namespace
    match_service: $match_service
    match_fingerprint: $match_fingerprint
    match_labels: $match_labels
    match_priority: $match_priority
    match_finding_type: $match_finding_type
    action_value: $action_value
    priority: $priority
    effective_until: $effective_until
    name: $name
    description: $description
    apply_to_existing: $apply_to_existing
  ) {
    success
    rule {
      id
      name
      rule_type
      action
      priority
      enabled
      match_alertname
      match_namespace
      match_service
      match_fingerprint
      match_source
      match_priority
      match_labels
      action_value
      effective_from
      effective_until
      created_at
      updated_at
    }
    bulk_operation {
      events_to_update
      status
    }
    error
  }
}
`;

export const EVENT_PREVIEW_TRIAGE_RULE = `
mutation EventPreviewTriageRule(
  $cloud_account_id: String!
  $rule_type: String!
  $action: String!
  $match_source: String
  $match_alertname: String
  $match_namespace: String
  $match_service: String
  $match_fingerprint: String
  $match_labels: String
  $match_priority: String
  $match_finding_type: String
) {
  event_preview_triage_rule(
    cloud_account_id: $cloud_account_id
    rule_type: $rule_type
    action: $action
    match_source: $match_source
    match_alertname: $match_alertname
    match_namespace: $match_namespace
    match_service: $match_service
    match_fingerprint: $match_fingerprint
    match_labels: $match_labels
    match_priority: $match_priority
    match_finding_type: $match_finding_type
  ) {
    matching_events_count
    sample_events {
      id
      title
      namespace
      service
    }
    new_status
  }
}
`;

export const EVENT_GET_TRIAGE_RULES = `
mutation EventGetTriageRules(
  $cloud_account_id: String
  $rule_type: String
  $enabled: Boolean
) {
  event_get_triage_rules(
    cloud_account_id: $cloud_account_id
    rule_type: $rule_type
    enabled: $enabled
  ) {
    rules {
      id
      account_id
      name
      description
      rule_type
      action
      action_value
      priority
      enabled
      match_alertname
      match_namespace
      match_service
      match_fingerprint
      match_source
      match_priority
      match_labels
      match_finding_type
      match_occurrence_greater_than
      effective_from
      effective_until
      is_editable
      can_override
      is_system_rule
      is_overridden
      match_count
      last_matched_at
      apply_to_existing
      created_by
      updated_by
      created_at
      updated_at
    }
  }
}
`;

export const EVENT_DELETE_TRIAGE_RULE = `
mutation EventDeleteTriageRule(
  $cloud_account_id: String!
  $rule_id: String!
  $hard_delete: Boolean
) {
  event_delete_triage_rule(
    cloud_account_id: $cloud_account_id
    rule_id: $rule_id
    hard_delete: $hard_delete
  ) {
    success
    error
  }
}
`;

export const EVENT_TOGGLE_SYSTEM_RULE_OVERRIDE = `
mutation EventToggleSystemRuleOverride(
  $cloud_account_id: String!
  $system_rule_id: String!
  $disabled: Boolean!
) {
  event_toggle_system_rule_override(
    cloud_account_id: $cloud_account_id
    system_rule_id: $system_rule_id
    disabled: $disabled
  ) {
    success
    error
    is_overridden
  }
}
`;

export const EVENT_UPDATE_NB_STATUS = `
mutation EventUpdateNBStatus(
  $event_id: String!
  $nb_status: String!
  $snoozed_until: String
) {
  event_update_nb_status(
    event_id: $event_id
    nb_status: $nb_status
    snoozed_until: $snoozed_until
  ) {
    success
    prev_status
    new_status
  }
}
`;

export const EVENT_GET_DUPLICATES = `
mutation EventGetDuplicates($event_id: String!) {
  event_get_duplicates(event_id: $event_id) {
    duplicates {
      event_id
      fingerprint
      occurrence_number
      first_event_id
      starts_at
    }
  }
}
`;

// Query to check if an event is a recurrence of a previously resolved event
// A recurrence is when: first_event_id = event_id (it's first in its chain) AND previous_event_id != event_id (has ref to old chain)
export const EVENT_GET_RECURRENCE_INFO = `
query EventGetRecurrenceInfo($event_id: String!) {
  event_get_recurrence_info(event_id: $event_id) {
    data {
      event_id
      first_event_id
      previous_event_id
      occurrence_number
    }
  }
}
`;

export const EVENT_GET_DUPLICATE_SUGGESTIONS = `
mutation EventGetDuplicateSuggestions($event_id: String!) {
  event_get_duplicate_suggestions(event_id: $event_id) {
    suggestions {
      event_id
      title
      starts_at
      occurrence_number
      is_first
    }
  }
}
`;

export const EVENT_GET_TRIAGE_RULE_EVENTS = `
mutation EventGetTriageRuleEvents(
  $rule_id: String!
  $account_id: String
  $limit: Int
  $offset: Int
  $start_date: String
  $end_date: String
) {
  event_get_triage_rule_events(
    rule_id: $rule_id
    account_id: $account_id
    limit: $limit
    offset: $offset
    start_date: $start_date
    end_date: $end_date
  ) {
    events {
      id
      account_id
      title
      subject_name
      subject_namespace
      subject_type
      priority
      status
      nb_status
      starts_at
      classified_at
      classification
    }
    total
    limit
    offset
  }
}
`;

export const EVENT_GET_THRESHOLD_SUGGESTION = `
mutation EventGetThresholdSuggestion($event_id: String!) {
  event_get_threshold_suggestion(event_id: $event_id) {
    available
    source
    alert_definition {
      metric_name
      metric_namespace
      operator
      current_threshold
      aggregation
      period
      evaluation_periods
      alarm_name
    }
    firing_analysis {
      total_occurrences
      time_range_days
      avg_firings_per_day
    }
    suggestion {
      suggested_threshold
      reason
      confidence
      metric_p50
      metric_p90
      metric_p95
      metric_p99
      estimated_reduction
      method
      recommendation_type
      suggested_duration
      duration_reason
    }
    alert_quality {
      classification
      recommendation
      firing_frequency
      resolution_rate
      engagement_rate
      flapping_rate
      transient_rate
      duration_p90
      instant_rate
    }
    error
  }
}
`;

export const EVENT_LIST_THRESHOLD_SUGGESTIONS = `
mutation EventListThresholdSuggestions(
  $cloud_account_id: String
  $source: String
  $confidence: String
  $limit: Int
  $offset: Int
) {
  event_list_threshold_suggestions(
    cloud_account_id: $cloud_account_id
    source: $source
    confidence: $confidence
    limit: $limit
    offset: $offset
  ) {
    suggestions {
      id
      alert_rule_key
      fingerprint
      cloud_account_id
      source
      alert_name
      metric_name
      metric_namespace
      current_threshold
      operator
      suggested_threshold
      reason
      confidence
      estimated_reduction
      method
      recommendation_type
      computed_at
      event_aggregation_key
      firing_analysis
      alert_quality
      metric_stats
      query_metadata
    }
    total
  }
}
`;

// -------------------- Types --------------------

export interface ClassifyEventInput {
  event_id: string;
  classification: 'true_positive' | 'false_positive' | 'benign_positive' | 'duplicate';
  reason_code: string;
  reason_text?: string;
  priority_direction?: 'too_high' | 'correct' | 'too_low';
  corrected_priority?: string;
  apply_scope: 'this_event' | 'this_fingerprint' | 'time_limited';
  apply_until_hours?: number;
  linked_event_id?: string;
  apply_to_existing: boolean;
  confirmed: boolean;
}

export interface ClassifyPreviewInput {
  event_id: string;
  classification: string;
  apply_scope: string;
  apply_until_hours?: number;
}

export interface CreateTriageRuleInput {
  cloud_account_id: string;
  rule_type: 'suppression' | 'scoring' | 'classification';
  action: string;
  match_alertname?: string;
  match_namespace?: string;
  match_service?: string;
  match_fingerprint?: string;
  match_source?: string;
  match_priority?: string;
  match_labels?: string;
  match_finding_type?: string;
  action_value?: string;
  priority?: number;
  effective_until?: string;
  name?: string;
  description?: string;
  apply_to_existing?: boolean;
}

export interface RulePreviewInput {
  cloud_account_id: string;
  rule_type: string;
  action: string;
  match_alertname?: string;
  match_namespace?: string;
  match_service?: string;
  match_fingerprint?: string;
  match_source?: string;
  match_priority?: string;
  match_labels?: string;
  match_finding_type?: string;
}

export interface RulePreviewResponse {
  matching_events_count: number;
  sample_events: {
    id: string;
    title: string;
    namespace?: string;
    service?: string;
  }[];
  new_status: string;
}

export interface GetTriageRulesInput {
  cloud_account_id?: string;
  rule_type?: string;
  enabled?: boolean;
}

export interface DeleteTriageRuleInput {
  cloud_account_id: string;
  rule_id: string;
  hard_delete: boolean;
}

export interface UpdateTriageRuleInput {
  cloud_account_id: string;
  rule_id: string;
  rule_type: 'suppression' | 'scoring' | 'classification';
  action: string;
  match_alertname?: string;
  match_namespace?: string;
  match_service?: string;
  match_fingerprint?: string;
  match_source?: string;
  match_priority?: string;
  match_labels?: string;
  match_finding_type?: string;
  action_value?: string;
  priority?: number;
  effective_until?: string;
  name?: string;
  description?: string;
  apply_to_existing?: boolean;
}

export interface UpdateNBStatusInput {
  event_id: string;
  nb_status: 'OPEN' | 'ACKNOWLEDGED' | 'INVESTIGATING' | 'ACTION_REQUIRED' | 'SNOOZED' | 'SUPPRESSED' | 'DROPPED' | 'DUPLICATE' | 'RESOLVED';
  snoozed_until?: string;
}

export interface TriageRule {
  id: string;
  account_id?: string;
  name?: string;
  description?: string;
  rule_type: string;
  action: string;
  action_value?: string;
  priority: number;
  enabled: boolean;
  match_alertname?: string;
  match_namespace?: string;
  match_service?: string;
  match_fingerprint?: string;
  match_source?: string;
  match_priority?: string;
  match_labels?: string;
  match_finding_type?: string;
  match_occurrence_greater_than?: number;
  effective_from?: string;
  effective_until?: string;
  is_editable: boolean;
  can_override: boolean;
  is_system_rule?: boolean;
  is_overridden?: boolean;
  match_count: number;
  last_matched_at?: string;
  apply_to_existing?: boolean;
  created_by?: string;
  updated_by?: string;
  created_at: string;
  updated_at: string;
}

export interface ToggleSystemRuleOverrideInput {
  cloud_account_id: string;
  system_rule_id: string;
  disabled: boolean;
}

export interface ClassifyPreviewResponse {
  current_event: {
    id: string;
    title: string;
    new_status: string;
  };
  existing_events: {
    count: number;
    sample_titles: string[];
    will_be_updated: boolean;
  };
  future_events: {
    rule_applies: boolean;
    scope_description: string;
  };
  rule_to_create?: {
    rule_type: string;
    match_criteria: string;
    action: string;
    expires_at?: string;
  };
}

export interface DuplicateSuggestion {
  event_id: string;
  title: string;
  starts_at: string;
  occurrence_number: number;
  is_first: boolean;
}

export interface GetTriageRuleEventsInput {
  rule_id: string;
  account_id?: string;
  limit?: number;
  offset?: number;
  start_date?: string;
  end_date?: string;
}

export interface TriageRuleEvent {
  id: string;
  account_id: string;
  title: string;
  subject_name?: string;
  subject_namespace?: string;
  subject_type?: string;
  priority?: string;
  status?: string;
  nb_status?: string;
  starts_at?: string;
  classified_at: string;
  classification: string;
}

export interface GetTriageRuleEventsResponse {
  events: TriageRuleEvent[];
  total: number;
  limit: number;
  offset: number;
}

// -------------------- Reason Codes --------------------

export const REASON_CODES = {
  true_positive: [
    { value: 'correct_severity', label: 'Correct Severity' },
    { value: 'wrong_service_tier', label: 'Wrong Service Tier' },
    { value: 'missing_dependency', label: 'Missing Dependency' },
  ],
  false_positive: [
    { value: 'known_noise', label: 'Known Noise' },
    { value: 'threshold_too_sensitive', label: 'Threshold Too Sensitive' },
    { value: 'test_alert', label: 'Test Alert' },
    { value: 'wrong_environment', label: 'Wrong Environment' },
  ],
  benign_positive: [
    { value: 'maintenance_window', label: 'Maintenance Window' },
    { value: 'expected_behavior', label: 'Expected Behavior' },
    { value: 'batch_job', label: 'Batch Job' },
    { value: 'deployment', label: 'Deployment' },
  ],
  duplicate: [{ value: 'duplicate_incident', label: 'Duplicate Incident' }],
};

export const NB_STATUS_OPTIONS = [
  { value: 'OPEN', label: 'Open' },
  { value: 'ACTION_REQUIRED', label: 'Action Required' },
  { value: 'SNOOZED', label: 'Snoozed' },
  { value: 'SUPPRESSED', label: 'Suppressed' },
  { value: 'DROPPED', label: 'Dropped' },
  { value: 'DUPLICATE', label: 'Duplicate' },
  { value: 'RESOLVED', label: 'Resolved' },
  // ACKNOWLEDGED and INVESTIGATING removed from UI but kept in backend for backwards compatibility
];

export const TRIAGE_STATUS_TOOLTIPS: Record<string, string> = {
  OPEN: 'No triage done yet. Awaiting review.',
  ACTION_REQUIRED: 'Triaged. Waiting on action from your team.',
  SNOOZED: 'Events paused temporarily. Will resume after the set duration.',
  SUPPRESSED: 'Events from this issue will not trigger alerts.',
  DROPPED: 'Issue dismissed and excluded from NudgeBee.',
  DUPLICATE: 'Automatically identified as a duplicate by NudgeBee.',
  RESOLVED: 'Issue has been resolved.',
};

export function getTriageStatusTooltip(status: string, snoozedUntil?: string): string {
  if (status === 'SNOOZED' && snoozedUntil) {
    const formatted = new Date(snoozedUntil).toLocaleString(undefined, {
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
    });
    return `Snoozed until ${formatted}`;
  }
  return TRIAGE_STATUS_TOOLTIPS[status] || '';
}

export const CLASSIFICATION_OPTIONS = [
  {
    value: 'true_positive',
    label: 'True Positive',
    description: 'This alert correctly identifies an issue that needs attention',
  },
  {
    value: 'false_positive',
    label: 'False Positive',
    description: 'This alert was triggered incorrectly - no actual issue exists',
  },
  {
    value: 'benign_positive',
    label: 'Benign Positive',
    description: 'This alert is correct but represents expected/acceptable behavior',
  },
  {
    value: 'duplicate',
    label: 'Duplicate',
    description: 'This is a duplicate of another event/incident',
  },
];

export const APPLY_SCOPE_OPTIONS = [
  {
    value: 'this_event',
    label: 'This Event Only',
    description: 'Apply classification to this specific event only',
  },
  {
    value: 'this_fingerprint',
    label: 'All Similar Events',
    description: 'Apply to all events with the same fingerprint (past and future)',
  },
  {
    value: 'time_limited',
    label: 'Time Limited',
    description: 'Apply to future events for a limited time period',
  },
];

// -------------------- API Functions --------------------

const apiTriage = {
  /**
   * Preview the impact of classifying an event before confirming
   */
  async classifyPreview(data: ClassifyPreviewInput) {
    try {
      const response = await queryGraphQL(EVENT_CLASSIFY_PREVIEW, 'EventClassifyPreview', {
        event_id: data.event_id,
        classification: data.classification,
        apply_scope: data.apply_scope,
        apply_until_hours: data.apply_until_hours,
      });
      return response?.data?.data?.event_classify_preview;
    } catch (error) {
      console.error('Failed to preview classification:', error);
      throw error;
    }
  },

  /**
   * Classify an event as TP/FP/BP/Duplicate
   */
  async classifyEvent(data: ClassifyEventInput) {
    try {
      const response = await queryGraphQL(EVENT_CLASSIFY, 'EventClassify', {
        event_id: data.event_id,
        classification: data.classification,
        reason_code: data.reason_code,
        reason_text: data.reason_text,
        priority_direction: data.priority_direction,
        corrected_priority: data.corrected_priority,
        apply_scope: data.apply_scope,
        apply_until_hours: data.apply_until_hours,
        linked_event_id: data.linked_event_id,
        apply_to_existing: data.apply_to_existing,
        confirmed: data.confirmed,
      });
      return response?.data?.data?.event_classify;
    } catch (error) {
      console.error('Failed to classify event:', error);
      throw error;
    }
  },

  /**
   * Get triage rules for an account
   */
  async getTriageRules(data: GetTriageRulesInput) {
    try {
      const response = await queryGraphQL(EVENT_GET_TRIAGE_RULES, 'EventGetTriageRules', {
        cloud_account_id: data.cloud_account_id,
        rule_type: data.rule_type,
        enabled: data.enabled,
      });
      return response?.data?.data?.event_get_triage_rules;
    } catch (error) {
      console.error('Failed to get triage rules:', error);
      throw error;
    }
  },

  /**
   * Create a new triage rule
   */
  async createTriageRule(data: CreateTriageRuleInput) {
    try {
      const response = await queryGraphQL(EVENT_CREATE_TRIAGE_RULE, 'EventCreateTriageRule', {
        cloud_account_id: data.cloud_account_id,
        rule_type: data.rule_type,
        action: data.action,
        match_source: data.match_source,
        match_alertname: data.match_alertname,
        match_namespace: data.match_namespace,
        match_service: data.match_service,
        match_fingerprint: data.match_fingerprint,
        match_labels: data.match_labels,
        match_priority: data.match_priority,
        match_finding_type: data.match_finding_type,
        action_value: data.action_value,
        priority: data.priority,
        effective_until: data.effective_until,
        name: data.name,
        description: data.description,
        apply_to_existing: data.apply_to_existing,
      });
      return response?.data?.data?.event_create_triage_rule;
    } catch (error) {
      console.error('Failed to create triage rule:', error);
      throw error;
    }
  },

  /**
   * Preview how many existing events would match a rule's criteria
   */
  async previewTriageRule(data: RulePreviewInput): Promise<RulePreviewResponse | null> {
    try {
      const response = await queryGraphQL(EVENT_PREVIEW_TRIAGE_RULE, 'EventPreviewTriageRule', {
        cloud_account_id: data.cloud_account_id,
        rule_type: data.rule_type,
        action: data.action,
        match_source: data.match_source,
        match_alertname: data.match_alertname,
        match_namespace: data.match_namespace,
        match_service: data.match_service,
        match_fingerprint: data.match_fingerprint,
        match_labels: data.match_labels,
        match_priority: data.match_priority,
        match_finding_type: data.match_finding_type,
      });
      return response?.data?.data?.event_preview_triage_rule;
    } catch (error) {
      console.error('Failed to preview triage rule:', error);
      throw error;
    }
  },

  /**
   * Delete (or disable) a triage rule
   */
  async deleteTriageRule(data: DeleteTriageRuleInput) {
    try {
      const response = await queryGraphQL(EVENT_DELETE_TRIAGE_RULE, 'EventDeleteTriageRule', {
        cloud_account_id: data.cloud_account_id,
        rule_id: data.rule_id,
        hard_delete: data.hard_delete,
      });
      return response?.data?.data?.event_delete_triage_rule;
    } catch (error) {
      console.error('Failed to delete triage rule:', error);
      throw error;
    }
  },

  /**
   * Toggle system rule override for an account (enable/disable a system rule for specific account)
   */
  async toggleSystemRuleOverride(data: ToggleSystemRuleOverrideInput) {
    try {
      const response = await queryGraphQL(EVENT_TOGGLE_SYSTEM_RULE_OVERRIDE, 'EventToggleSystemRuleOverride', {
        cloud_account_id: data.cloud_account_id,
        system_rule_id: data.system_rule_id,
        disabled: data.disabled,
      });
      return response?.data?.data?.event_toggle_system_rule_override;
    } catch (error) {
      console.error('Failed to toggle system rule override:', error);
      throw error;
    }
  },

  /**
   * Update an existing triage rule
   */
  async updateTriageRule(data: UpdateTriageRuleInput) {
    try {
      const response = await queryGraphQL(EVENT_UPDATE_TRIAGE_RULE, 'EventUpdateTriageRule', {
        cloud_account_id: data.cloud_account_id,
        rule_id: data.rule_id,
        rule_type: data.rule_type,
        action: data.action,
        match_source: data.match_source,
        match_alertname: data.match_alertname,
        match_namespace: data.match_namespace,
        match_service: data.match_service,
        match_fingerprint: data.match_fingerprint,
        match_labels: data.match_labels,
        match_priority: data.match_priority,
        match_finding_type: data.match_finding_type,
        action_value: data.action_value,
        priority: data.priority,
        effective_until: data.effective_until,
        name: data.name,
        description: data.description,
        apply_to_existing: data.apply_to_existing,
      });
      return response?.data?.data?.event_update_triage_rule;
    } catch (error) {
      console.error('Failed to update triage rule:', error);
      throw error;
    }
  },

  /**
   * Update the NB status of an event
   */
  async updateNBStatus(data: UpdateNBStatusInput) {
    try {
      const response = await queryGraphQL(EVENT_UPDATE_NB_STATUS, 'EventUpdateNBStatus', {
        event_id: data.event_id,
        nb_status: data.nb_status,
        snoozed_until: data.snoozed_until,
      });
      return response?.data?.data?.event_update_nb_status;
    } catch (error) {
      console.error('Failed to update NB status:', error);
      throw error;
    }
  },

  /**
   * Get duplicate suggestions for an event
   */
  async getDuplicates(eventId: string) {
    try {
      const response = await queryGraphQL(EVENT_GET_DUPLICATE_SUGGESTIONS, 'EventGetDuplicateSuggestions', {
        event_id: eventId,
      });
      return response?.data?.data?.event_get_duplicate_suggestions;
    } catch (error) {
      console.error('Failed to get duplicates:', error);
      throw error;
    }
  },

  /**
   * Get events that matched a specific triage rule
   */
  async getTriageRuleEvents(data: GetTriageRuleEventsInput): Promise<GetTriageRuleEventsResponse | null> {
    try {
      const response = await queryGraphQL(EVENT_GET_TRIAGE_RULE_EVENTS, 'EventGetTriageRuleEvents', {
        rule_id: data.rule_id,
        account_id: data.account_id,
        limit: data.limit,
        offset: data.offset,
        start_date: data.start_date,
        end_date: data.end_date,
      });
      return response?.data?.data?.event_get_triage_rule_events;
    } catch (error) {
      console.error('Failed to get triage rule events:', error);
      throw error;
    }
  },

  /**
   * Get threshold suggestion for a single event
   */
  async getThresholdSuggestion(eventId: string): Promise<ThresholdSuggestionResponse | null> {
    try {
      const response = await queryGraphQL(EVENT_GET_THRESHOLD_SUGGESTION, 'EventGetThresholdSuggestion', {
        event_id: eventId,
      });
      return response?.data?.data?.event_get_threshold_suggestion;
    } catch (error) {
      console.error('Failed to get threshold suggestion:', error);
      return null;
    }
  },

  /**
   * List all cached threshold suggestions
   */
  async listThresholdSuggestions(data: ListThresholdSuggestionsInput): Promise<ListThresholdSuggestionsResponse | null> {
    try {
      const response = await queryGraphQL(EVENT_LIST_THRESHOLD_SUGGESTIONS, 'EventListThresholdSuggestions', {
        cloud_account_id: data.cloud_account_id,
        source: data.source,
        confidence: data.confidence,
        limit: data.limit,
        offset: data.offset,
      });
      return response?.data?.data?.event_list_threshold_suggestions;
    } catch (error) {
      console.error('Failed to list threshold suggestions:', error);
      return null;
    }
  },

  /**
   * Get recurrence info for an event
   * Returns info about previous chain if this event is a recurrence of a resolved event
   */
  async getRecurrenceInfo(eventId: string): Promise<RecurrenceInfo | null> {
    try {
      const response = await queryGraphQL(EVENT_GET_RECURRENCE_INFO, 'EventGetRecurrenceInfo', {
        event_id: eventId,
      });
      const data = response?.data?.data?.event_get_recurrence_info?.data?.[0];
      if (!data) {
        return null;
      }

      // Check if this is a recurrence: first_event_id = event_id AND previous_event_id != event_id
      const isRecurrence = data.first_event_id === data.event_id && data.previous_event_id !== data.event_id;

      if (!isRecurrence) {
        return null;
      }

      return {
        isRecurrence: true,
        previousEventId: data.previous_event_id,
      };
    } catch (error) {
      console.error('Failed to get recurrence info:', error);
      return null;
    }
  },
};

// -------------------- Threshold Suggestion Types --------------------

export interface ThresholdSuggestionResponse {
  available: boolean;
  source: string;
  alert_definition?: {
    metric_name: string;
    metric_namespace: string;
    operator: string;
    current_threshold: number;
    aggregation: string;
    period: number;
    evaluation_periods: number;
    alarm_name: string;
  };
  firing_analysis?: {
    total_occurrences: number;
    time_range_days: number;
    avg_firings_per_day: number;
  };
  suggestion?: {
    suggested_threshold: number;
    reason: string;
    confidence: string;
    metric_p50: number;
    metric_p90: number;
    metric_p95: number;
    metric_p99: number;
    estimated_reduction: number;
    method?: string;
    recommendation_type?: string;
    suggested_duration?: number;
    duration_reason?: string;
  };
  alert_quality?: {
    classification: string;
    recommendation: string;
    firing_frequency: number;
    resolution_rate: number;
    engagement_rate: number;
    flapping_rate: number;
    transient_rate: number;
    duration_p90: number;
    instant_rate: number;
  };
  error?: string;
}

export interface ThresholdSuggestionItem {
  id: string;
  alert_rule_key: string;
  fingerprint: string;
  cloud_account_id: string;
  source: string;
  alert_name?: string;
  metric_name?: string;
  metric_namespace?: string;
  current_threshold?: number;
  operator?: string;
  suggested_threshold?: number;
  reason?: string;
  confidence?: string;
  estimated_reduction?: number;
  method?: string;
  recommendation_type?: string;
  computed_at?: string;
  event_aggregation_key?: string;
  firing_analysis?: {
    total_occurrences: number;
    time_range_days: number;
    avg_firings_per_day: number;
    metric_values_at_firing: number[];
  };
  alert_quality?: {
    classification: string;
    recommendation: string;
    firing_frequency: number;
    resolution_rate: number;
    engagement_rate: number;
    flapping_rate: number;
    transient_rate: number;
    duration_p90: number;
    instant_rate: number;
  };
  metric_stats?: {
    p50: number;
    p90: number;
    p95: number;
    p99: number;
    median: number;
    mad: number;
    recommendation_type?: string;
    suggested_duration?: number;
    duration_reason?: string;
    risk_level?: string;
    risk_warnings?: string[];
  };
  query_metadata?: {
    metric_provider: string;
    service_name?: string;
    region?: string;
    metric_names?: string[];
    metric_namespace?: string;
    dimensions?: Array<Record<string, string>>;
    statistics?: string[];
    resource_ids?: string[];
    promql?: string;
  };
}

export interface ListThresholdSuggestionsInput {
  cloud_account_id?: string;
  source?: string;
  confidence?: string;
  limit?: number;
  offset?: number;
}

export interface ListThresholdSuggestionsResponse {
  suggestions: ThresholdSuggestionItem[];
  total: number;
}

// Types for recurrence info
export interface RecurrenceInfo {
  isRecurrence: boolean;
  previousEventId: string;
}

export default apiTriage;
