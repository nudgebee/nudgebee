import KubernetesClusterUpgradeRecommendation from '@components1/recommendations/KubernetesClusterUpgradeRecommendation';
import TraceIcon from '@assets/kubernetes/trace-icon.svg';
import recommendationApi from '@api1/recommendation';

class DeprecatedApis {
  constructor(options = {}) {
    this.id = 'DeprecatedApis';
    this.icon = TraceIcon;
    this.text = 'Check for Deprecated APIs';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.accountId = '';
    this.version = '';
    this.disableInfographics = options.disabledInfographic || false;
  }

  canRenderContent = async (accountId, version) => {
    this.accountId = accountId;
    this.version = version;

    try {
      const res = await recommendationApi.getK8sRecommendation({
        accountId: accountId,
        category: 'InfraUpgrade',
        ruleName: 'k8s_api_deprecated',
        status: ['Open'],
        recommendation: null,
        limit: 100,
        offset: 0,
        fetchTicket: false,
      });

      const recommendations = res?.data?.recommendation || [];

      if (recommendations.length > 0) {
        const currentVersionMajorMinor = this.version ? parseFloat(this.version.replace('v', '')) : 0;

        const deprecatedInTargetOrHigher = recommendations.filter((item) => {
          if (item.recommendation?.deprecated_items?.length > 0) {
            const deprecatedVersion = item.recommendation?.deprecated_version;
            if (deprecatedVersion && currentVersionMajorMinor > 0) {
              const deprecatedVersionNum = parseFloat(deprecatedVersion.replace('v', ''));
              return deprecatedVersionNum <= currentVersionMajorMinor;
            }
            return true;
          }
          return false;
        });

        const highestSeverityItems = recommendations.filter((item) => item.severity === 'Highest' || item.severity === 'Critical');
        const deprecatedInLowerVersions = recommendations.filter((item) => {
          const deprecatedVersion = item.recommendation?.deprecated_version;
          if (deprecatedVersion && currentVersionMajorMinor > 0) {
            const deprecatedVersionNum = parseFloat(deprecatedVersion.replace('v', ''));
            return (
              deprecatedVersionNum < currentVersionMajorMinor &&
              (item.recommendation?.deprecated_items?.length > 0 || item.recommendation?.deleted_items?.length > 0)
            );
          }
          return false;
        });

        if (deprecatedInLowerVersions.length > 0) {
          this.insightData.push({
            message: `${deprecatedInLowerVersions.length} API(s) deprecated in earlier versions are still in use and require immediate attention`,
            component: null,
            severity: 'Critical',
          });
        }

        if (deprecatedInTargetOrHigher.length > 0) {
          this.insightData.push({
            message: `Found ${deprecatedInTargetOrHigher.length} deprecated API(s) that need attention`,
            component: null,
            severity: 'Medium',
          });
        }

        const deletedInTargetOrHigher = recommendations.filter((item) => {
          if (item.recommendation?.deleted_items?.length > 0) {
            const deletedVersion = item.recommendation?.deleted_version;
            if (deletedVersion && currentVersionMajorMinor > 0) {
              const deletedVersionNum = parseFloat(deletedVersion.replace('v', ''));
              return deletedVersionNum <= currentVersionMajorMinor;
            }
            return true;
          }
          return false;
        });

        if (deletedInTargetOrHigher.length > 0) {
          this.insightData.push({
            message: `Found ${deletedInTargetOrHigher.length} API(s) that will be deleted in or before the target version`,
            component: null,
            severity: 'Critical',
          });
        }

        if (highestSeverityItems.length > 0) {
          this.insightData.push({
            message: `${highestSeverityItems.length} critical API compatibility issue(s) detected`,
            component: null,
            severity: 'Critical',
          });
        }

        this.renderContent = true;
      }
    } catch (error) {
      console.error('Error fetching deprecated API recommendations:', error);
      this.renderContent = false;
    }

    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderDeprecatedApis()];
  };

  renderDeprecatedApis = () => {
    return (
      <KubernetesClusterUpgradeRecommendation
        kubernetes={{ id: this.accountId, version: this.version }}
        heading={''}
        disableInfographics={this.disableInfographics}
      />
    );
  };
}

export default DeprecatedApis;
