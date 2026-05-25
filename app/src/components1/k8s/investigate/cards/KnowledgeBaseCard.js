import apiKubernetes from '@api1/kubernetes';
import MarkDowns from '@components1/common/MarkDowns';
import DescriptionIcon from '@assets/investigation/description-icon.svg';

class KnowledgeBaseCard {
  constructor() {
    this.id = 'KnowledgeBaseCard';
    this.icon = DescriptionIcon;
    this.text = 'Description';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.knowledgeBase = [];
  }

  canRenderContent = async (evidenceData, troubleShootingEvent) => {
    if (troubleShootingEvent?.aggregation_key) {
      try {
        let res = await apiKubernetes.getKnowledgeBase(troubleShootingEvent?.aggregation_key);
        if (res?.data) {
          this.knowledgeBase = res.data;
          if (res.data && res.data.length > 0) {
            this.renderContent = true;
          }
        }
      } catch (e) {
        console.error(e);
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderKnowledgeBank()];
  };

  renderKnowledgeBank = () => {
    if (this.knowledgeBase && this.knowledgeBase.length > 0) {
      return this.knowledgeBase.map((base) => {
        let data = '';
        if (base.description) {
          data = data + base.description;
        }
        if (base.impact) {
          data = data + '\n\n' + base.impact;
        }
        if (base.diagnosis) {
          data = data + '\n\n' + base.diagnosis;
        }
        if (base.mitigation) {
          data = data + '\n\n' + base.mitigation;
        }
        return <MarkDowns key={'description'} data={data} sx={{ maxHeight: '', width: '100%', overflowY: '' }} />;
      });
    }
  };
}

export default KnowledgeBaseCard;
