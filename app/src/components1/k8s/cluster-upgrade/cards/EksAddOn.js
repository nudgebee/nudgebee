import TraceIcon from '@assets/kubernetes/trace-icon.svg';
import recommendationApi from '@api1/recommendation';
import { ListingLayout } from '@components1/ds/ListingLayout';
import DownloadButton from '@common-new/DownloadButton';
import CustomTable from '@common-new/tables/CustomTable2';
import Tooltip from '@components1/ds/Tooltip';
import { Box } from '@mui/material';
import { Label } from '@components1/ds/Label';

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
              component: <Label text={item.recommendation?.current_compatible ? 'Compatible' : 'Incompatible'} />,
            },
            {
              component: <Label text={item.recommendation?.target_compatible ? 'Compatible' : 'Incompatible'} />,
            },
            {
              component: (
                <Tooltip title={item.recommendation?.supported_versions?.join(', ') || 'No versions available'}>
                  <Box>{item.recommendation?.supported_versions?.length > 0 ? `${item.recommendation.supported_versions.length} versions` : '-'}</Box>
                </Tooltip>
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
      <ListingLayout id='eks-add-on-box'>
        <ListingLayout.Toolbar actions={<DownloadButton onClick={() => ({ tableId: 'eks-add-on-list-table' })} />} />
        <ListingLayout.Body>
          <CustomTable
            id={'eks-add-on-list-table'}
            tableData={this.eksAddOnData}
            headers={['Name', 'EKS Add-on Name', 'Current version', 'Current Compatibility', 'Target Compatibility', 'Target Supported Versions']}
            rowsPerPage={this.eksAddOnData.length}
            loading={false}
          />
        </ListingLayout.Body>
      </ListingLayout>
    );
  };
}

export default EksAddOn;
