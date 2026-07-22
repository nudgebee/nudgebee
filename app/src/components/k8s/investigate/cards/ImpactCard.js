import ServiceMapIcon from '@assets/kubernetes/monitoring/service-map-icon.icon.svg';
import ImpactPanel from '@shared/widgets/ImpactPanel';

// ImpactCard surfaces the topology-driven correlation for an incident: the root subject,
// the correlated downstream services actively alerting in the window, and potential
// (topology-only) impact. Mirrors TimelineCard — a live-RPC card driven by event_get_impact.
class ImpactCard {
  constructor(event) {
    this.id = 'ImpactCard';
    this.icon = ServiceMapIcon;
    this.text = 'Impacted Services';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = true;
    this.event = event;
  }

  canRenderContent = async () => this.renderContent;

  getHighLightsData = () => this.insightData;

  getContentComponents = () => [() => this.renderImpact()];

  renderImpact = () => <ImpactPanel eventId={this.event.id} />;
}

export default ImpactCard;
