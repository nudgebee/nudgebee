import KubernetesPodProfiler from '@components1/k8s/details/KubernetesPodProfiler';
import PodProfileIcon from '@assets/investigation/pod-profile.svg';

class SVGImageCard {
  constructor() {
    this.id = 'SVGImageCard';
    this.icon = PodProfileIcon;
    this.text = 'Pod Profile';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.svgObject = '';
  }

  canRenderContent = async (evidenceData, event) => {
    this.event = event;
    const filterEvidence = evidenceData.filter((item) => {
      const { type, filename = '' } = item;

      if (type === 'svg') {
        return true;
      }

      if (type === 'gz') {
        return (
          filename.endsWith('svg.gz') ||
          filename.endsWith('pprof.svg.gz') ||
          filename.endsWith('txt.gz') ||
          filename.endsWith('jfr.gz') ||
          filename.endsWith('pprof.gz')
        );
      }

      return false;
    });
    if (filterEvidence.length > 0) {
      this.svgObject = [
        {
          evidence: [
            {
              data: JSON.stringify(filterEvidence),
            },
          ],
        },
      ];
      this.renderContent = true;
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderSVG()];
  };

  renderSVG = () => {
    return <KubernetesPodProfiler accountId={this.event.cloud_account_id} query={{}} findings={this.svgObject} readOnlyMode={true} />;
  };
}

export default SVGImageCard;
