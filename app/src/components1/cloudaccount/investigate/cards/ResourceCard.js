import CubeIcon from '@assets/kubernetes/cube-icon.svg';
import MarkDowns from '@components1/common/MarkDowns';
import { Box } from '@mui/material';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { Text } from '@components1/common';
import { ds } from '@utils/colors';

class ResourceCard {
  constructor(evidenceData, index) {
    this.id = `Resource_${index}`; // unique per card
    this.text = evidenceData?.additional_info?.title || 'Cloud Resource';
    this.icon = CubeIcon;
    this.enricherData = evidenceData;
  }

  async canRenderContent() {
    return !!this.enricherData?.data;
  }

  getHighLightsData = () => {
    return this.enricherData?.insight || [];
  };

  getContentComponents = () => {
    return [() => this.renderCardContent(this.enricherData)];
  };

  renderCardContent = (rawEventData) => {
    let tableData = [];
    if (typeof rawEventData.data === 'string') {
      try {
        tableData = JSON.parse(rawEventData.data);
      } catch {
        tableData = [];
      }
    } else {
      tableData = rawEventData.data;
    }
    if (!Array.isArray(tableData)) {
      tableData = [];
    }

    let tableRows = tableData.map((r) => {
      const data = [];
      data.push({
        component: <Text value={r['name']} />,
        drilldownQuery: r,
      });
      data.push({
        component: <Text value={r['type']} />,
      });
      data.push({
        component: <Text value={r['service_name']} />,
      });
      data.push({
        component: <Text value={r['status']} />,
      });
      data.push({
        component: <Text value={r['region']} />,
      });
      return data;
    });
    return (
      <Box sx={{ p: 2 }}>
        <CustomTable2
          headers={['Name', 'Type', 'ServiceName', 'Status', 'Region']}
          tableData={tableRows}
          rowsPerPage={tableData.length}
          showExpandable={true}
          expandable={{
            tabs: [
              {
                text: 'Details',
                value: 0,
                key: 'ec2-details',
                componentFn: function (_opt, drilldownQuery, _row) {
                  let jsonData = JSON.stringify(drilldownQuery, null, 2);
                  let markdown = '```json\n' + jsonData + '\n```\n';
                  return (
                    <Box sx={{ mb: ds.space[5], maxWidth: '100%', overflow: 'hidden' }}>
                      <MarkDowns data={markdown} />
                    </Box>
                  );
                },
              },
            ],
          }}
        />
      </Box>
    );
  };
}

export default ResourceCard;
