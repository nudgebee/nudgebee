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
      // The default is for a type this card does not know: say nothing it cannot stand behind.
      let message = result.suggestion?.reason || 'Alert Tuning has a verdict for this alert.';
      let severity = 'Info';
      if (recType === 'tune_threshold') {
        message = 'This alert may have a threshold that can be tuned to reduce noise.';
      } else if (recType === 'investigate_signal') {
        message = 'This alert is firing on real behaviour — the analysis says to investigate it rather than change its threshold.';
        severity = 'Warning';
      } else if (recType === 'insufficient_data') {
        message = result.suggestion?.reason || "There is not enough data to judge this alert's threshold yet.";
      } else if (recType === 'increase_duration') {
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
        message = result.suggestion?.reason || 'This alert has no threshold to tune.';
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
