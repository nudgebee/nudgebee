import { AlertManagerIcon } from '@assets';
import apiTriage from '@api1/triage';
import ThresholdSuggestionContent from './ThresholdSuggestionContent';

const SUPPORTED_SOURCES = [
  'AWS_CloudWatch_Alarm',
  'azure_monitor_webhook',
  'Azure_Monitor_Alert',
  'prometheus',
  'GCP_Metric_Alert',
  'pagerduty_webhook',
];

class ThresholdSuggestionCard {
  constructor(_evidence, data, index) {
    this.id = `ThresholdSuggestionCard_${index}`;
    this.text = 'Threshold Suggestion';
    this.icon = AlertManagerIcon;
    this.enricherData = data;
    this.suggestionData = null;
    this.insightData = [];
  }

  async canRenderContent(_evidences, event) {
    const source = event?.source || event?.labels?.source;
    if (!source || !SUPPORTED_SOURCES.includes(source)) {
      return false;
    }
    try {
      const result = await apiTriage.getThresholdSuggestion(event.id);
      if (!result?.available || !result?.suggestion) {
        return false;
      }
      this.suggestionData = result;
      const recType = result.suggestion?.recommendation_type;
      let message = 'This alert may have a threshold that can be tuned to reduce noise.';
      let severity = 'Info';
      if (recType === 'increase_duration') {
        message = 'This alert fires on transient spikes — consider increasing the evaluation window.';
      } else if (recType === 'tune_both') {
        message = 'This alert can be improved by adjusting both threshold and evaluation window.';
      } else if (recType === 'disable') {
        message = 'This alert appears broken — consider investigating or disabling it.';
        severity = 'Warning';
      } else if (recType === 'review_alert') {
        message = 'This alert fires on normal behavior — the alert definition itself needs review, not just the threshold.';
        severity = 'Warning';
      } else if (recType === 'not_eligible') {
        const reason = result.suggestion?.reason || '';
        if (reason.includes('metric query returned no data')) {
          message = 'Threshold tuning is not available — the metric data required for analysis could not be retrieved from your monitoring system.';
        } else {
          message = reason || 'This alert type is not eligible for threshold tuning.';
        }
      } else if (recType === 'none') {
        message = 'This alert threshold appears correctly tuned.';
      }
      this.insightData = [
        {
          message,
          severity,
        },
      ];
      return true;
    } catch {
      return false;
    }
  }

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => <ThresholdSuggestionContent data={this.suggestionData} />];
  };
}

export default ThresholdSuggestionCard;
