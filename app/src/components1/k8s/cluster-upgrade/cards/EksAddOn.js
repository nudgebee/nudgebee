import TraceIcon from '@assets/kubernetes/trace-icon.svg';
import recommendationApi from '@api1/recommendation';
import BoxLayout2 from '@components1/common/BoxLayout2';
import CustomTable from '@components1/common/tables/CustomTable2';
import CustomTooltip from '@components1/common/CustomTooltip';
import { Box } from '@mui/material';
import CustomLabels from '@components1/common/widgets/CustomLabels';

class EksAddOn {
  constructor() {
    this.id = 'EksAddOn';
    this.icon = TraceIcon;
    this.text = 'EKS Add-On';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.accountId = '';
    this.eksAddOnData = [];
  }

  canRenderContent = async (accountId) => {
    this.accountId = accountId;
    try {
      const addOnType = [];
      const res = await recommendationApi.getK8sRecommendation({
        accountId: accountId,
        ruleName: 'eks_add_ons_version',
        category: 'InfraUpgrade',
        status: ['Open'],
        recommendation: null,
        limit: 100,
        offset: 0,
        fetchTicket: false,
      });
      const data = res?.data?.recommendation || [];
      if (data.length > 0) {
        const tableData = data.map((item) => {
          addOnType.push(item.recommendation?.addon_name || '');
          return [
            {
              text: item.recommendation?.addon_name || '-',
            },
            {
              text: item.recommendation?.eks_addon_name || '-',
            },
            {
              text: item.recommendation?.current_version || '-',
            },
            {
              component: <CustomLabels text={item.recommendation?.current_compatible ? 'Compatible' : 'Incompatible'} />,
            },
            {
              component: <CustomLabels text={item.recommendation?.target_compatible ? 'Compatible' : 'Incompatible'} />,
            },
            {
              component: (
                <CustomTooltip title={item.recommendation?.supported_versions?.join(', ') || 'No versions available'}>
                  <Box sx={{ cursor: 'help' }}>
                    {item.recommendation?.supported_versions?.length > 0 ? `${item.recommendation.supported_versions.length} versions` : '-'}
                  </Box>
                </CustomTooltip>
              ),
            },
          ];
        });
        this.eksAddOnData = tableData;
        const incompatibleAddOns = data.filter(
          (item) => item.recommendation?.target_compatible === false || item.recommendation?.current_compatible === false
        );
        const message = incompatibleAddOns.length > 0 ? `Incompatible addon version for - ${addOnType.join(', ')}` : '';
        const component = null;
        if (message && incompatibleAddOns.length > 0) {
          this.insightData.push({
            message,
            component,
            severity: 'Critical',
          });
        }
        this.renderContent = true;
      }
    } catch {
      this.renderContent = false;
    }

    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderEksAddOn()];
  };

  renderEksAddOn = () => {
    return (
      <BoxLayout2
        id='eks-add-on-box'
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: 'eks-add-on-list-table',
              };
            },
          },
          sharing: { enabled: true },
        }}
      >
        <CustomTable
          id={'eks-add-on-list-table'}
          tableData={this.eksAddOnData}
          headers={['Name', 'EKS Add-on Name', 'Current version', 'Current Compatibility', 'Target Compatibility', 'Target Supported Versions']}
          rowsPerPage={this.eksAddOnData.length}
          loading={false}
        />
      </BoxLayout2>
    );
  };
}

export default EksAddOn;
