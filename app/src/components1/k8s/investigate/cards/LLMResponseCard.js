import DescriptionIcon from '@assets/kubernetes/description-icon.svg';
import KubernetesLLMResponseGenerator from '@components1/llm/KubernetesLLMResponseGeneratorForTabs';
import { getAssistantName } from '@hooks/useTenantBranding';

class LLMResponseCard {
  constructor(data, event) {
    this.id = 'LLMResponseCard';
    this.icon = DescriptionIcon;
    this.text = data?.additional_info?.title || `${getAssistantName()} Analysis`;
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.line = '';
    this.event = event;
    this.onDataUpdate = null;
    this.sessionId = '';
    this.enricherData = data;
    this.refreshRenderId = 0;
  }

  // Method to set data update callback
  setDataUpdateCallback(callback) {
    this.onDataUpdate = callback;
    this.refreshRenderId += 1;
  }

  canRenderContent = async () => {
    if (this.enricherData) {
      const sessionId = this.enricherData?.llm_response?.session_id || '';
      this.sessionId = sessionId;
      if (this.sessionId) {
        this.renderContent = true;
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderAskAI()];
  };

  renderAskAI = () => {
    const LLMConversationComponent = () => {
      return <KubernetesLLMResponseGenerator accountId={this.event.cloud_account_id} sessionId={this.sessionId} popup={true} />;
    };
    return <LLMConversationComponent />;
  };
}

export default LLMResponseCard;
