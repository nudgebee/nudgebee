// TextEnricherDynamicCard.js
import CubeIcon from '@assets/kubernetes/cube-icon.svg';
import CloudAccountTable from '@components1/cloudaccount/CloudAccountTable';
import { Text } from '@components1/common';
import Datetime from '@components1/common/format/Datetime';
import { Box } from '@mui/material';

class CloudWatchLogCard {
  constructor(_evidence, data, index) {
    this.id = `CloudWatchLogCard_${index}`; // unique per card
    this.text = `CloudWatch Logs`;
    this.icon = CubeIcon;
    this.enricherData = data;
  }

  async canRenderContent() {
    return this.enricherData?.evidences?.some((e) => e.additional_info?.action_type === 'aws_get_log');
  }

  getHighLightsData = () => {
    return this.enricherData?.insight || [];
  };

  getContentComponents = () => {
    let rawEvent = this.enricherData?.evidences?.filter((e) => e.additional_info?.action_type === 'aws_get_log');
    return [() => this.renderCardContent(rawEvent?.[0], this.enricherData)];
  };

  renderCardContent = (rawEventData, _data) => {
    const TABLE_COLUMNS = [{ name: 'Timestamp', width: '10%' }, 'Log'];
    let tableRows = [];
    try {
      let jsonMap = JSON.parse(rawEventData.data);
      tableRows = jsonMap?.results?.map((r) => {
        return [
          {
            component: <Datetime value={r.Timestamp} />,
            data: r,
          },
          {
            component: <Text showAutoEllipsis value={r.Message} sx={{ minWidth: '120px' }} />,
          },
        ];
      });
    } catch (e) {
      console.error('unable to parse data', e);
    }
    return (
      <Box sx={{ p: 2 }}>
        <CloudAccountTable
          id={'EventsLogTable'}
          headers={TABLE_COLUMNS}
          data={tableRows}
          rowsPerPage={tableRows.length}
          totalRows={tableRows.length}
          showExpandable={false}
        />
      </Box>
    );
  };
}

export default CloudWatchLogCard;
