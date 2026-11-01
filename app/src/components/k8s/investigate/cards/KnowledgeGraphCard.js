import KnowledgeGraphMap from '@components/k8s/details/KnowledgeGraphMap';
import ServiceMapIcon from '@assets/kubernetes/monitoring/service-map-icon.icon.svg';
import { summarizeDependencies } from './cardSummaries';

class KnowledgeGraphCard {
  constructor(evidenceData, event, index) {
    this.id = `KnowledgeGraphCard_${index}`;
    this.icon = ServiceMapIcon;
    this.text = evidenceData?.additional_info?.title || 'Service Dependencies';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.kgNodes = [];
    this.kgEdges = [];
    this.targetService = '';
    this.evidenceData = evidenceData;
    this.event = event;
  }

  async canRenderContent() {
    try {
      // KG evidence stores nodes/edges at top level (MarshalStructToMap flattens the struct)
      const ev = this.evidenceData;
      const nodes = ev?.nodes;
      const edges = ev?.edges;

      if (!Array.isArray(nodes) || nodes.length === 0) {
        return false;
      }

      this.kgNodes = nodes;
      this.kgEdges = Array.isArray(edges) ? edges : [];
      this.targetService = ev?.target_service || this.event?.subject_owner || '';
      // The server's own summary of the graph is not stored with this evidence, so it is read off
      // the graph here.
      this.insightData = ev?.insight?.length > 0 ? ev.insight : summarizeDependencies(this.kgNodes, this.kgEdges, this.targetService);
      this.renderContent = true;
      return true;
    } catch (error) {
      console.error('KnowledgeGraphCard: Error parsing evidence data:', error);
      return false;
    }
  }

  getHighLightsData() {
    return Array.isArray(this.insightData) ? this.insightData : [];
  }

  getContentComponents() {
    return [() => this.renderGraph()];
  }

  renderGraph() {
    return <KnowledgeGraphMap nodes={this.kgNodes} edges={this.kgEdges} targetService={this.targetService} />;
  }
}

export default KnowledgeGraphCard;
