import ServiceMapIcon from '@assets/kubernetes/monitoring/service-map-icon.icon.svg';
import apiKubernetes1 from '@api1/kubernetes1';
import ImpactPanel from '@shared/widgets/ImpactPanel';

// buildInsight turns the assembled incident into the one-line highlights shown on the
// collapsed card, so the header says what is actually inside instead of the generic
// "Nothing major to show". Plain sentences — each entry renders as its own line.
const buildInsight = (data) => {
  const a = data && data.assembly;
  if (!a) {
    return [];
  }
  const name = (data.seed && data.seed.name) || 'this service';
  const core = (a.same_incident || []).length;
  const causes = ((a.cause && a.cause.config_changes) || []).length + ((a.cause && a.cause.upstream) || []).length;
  const affected = (a.impact || []).length;
  const noise = (a.chronic || []).length;
  const noCoverage = !data.resolved || data.coverage_confidence === 'none' || data.coverage_confidence === 'low';

  const out = [];
  if (core > 0) {
    out.push(`${core} more alert${core === 1 ? '' : 's'} on ${name} — same problem`);
  }
  if (causes > 0) {
    out.push(`${causes} possible cause${causes === 1 ? '' : 's'} just before it started`);
  }
  if (affected > 0) {
    out.push(`${affected} service${affected === 1 ? '' : 's'} broke after this one`);
  } else if (noCoverage) {
    out.push('No service map for this one — we can’t tell what it affected');
  } else {
    out.push('Nothing that depends on this one alerted');
  }
  if (noise > 0) {
    out.push(`${noise} alert${noise === 1 ? '' : 's'} set aside as background noise`);
  }
  return out;
};

// ImpactCard surfaces the assembled incident: the other alerts about the same service,
// what probably caused it, what it affected, and the background noise it set aside.
// Driven by event_get_impact (assembly). Mirrors TimelineCard.
class ImpactCard {
  constructor(event) {
    this.id = 'ImpactCard';
    this.icon = ServiceMapIcon;
    // "Impacted Services" described only one of the four sections and made people skip
    // the card as a downstream-only list. It now holds the whole incident: the same
    // service's other alerts, the likely cause, what it affected, and the noise it set aside.
    this.text = 'This incident';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = true;
    this.event = event;
    this.impact = null;
  }

  // The page awaits this while building the card list, so it is where the assembly is
  // fetched: it lets the collapsed card show a real summary, and it makes expanding
  // instant because the panel reuses this result instead of issuing a second request.
  canRenderContent = async () => {
    try {
      const res = await apiKubernetes1.getImpactData(this.event.id);
      this.impact = res?.data?.data?.event_get_impact || null;
      this.insightData = buildInsight(this.impact);
    } catch (error) {
      console.error('Error fetching incident assembly:', error);
    }
    return this.renderContent;
  };

  getHighLightsData = () => this.insightData;

  getContentComponents = () => [() => this.renderImpact()];

  renderImpact = () => <ImpactPanel eventId={this.event.id} prefetched={this.impact} />;
}

export default ImpactCard;
