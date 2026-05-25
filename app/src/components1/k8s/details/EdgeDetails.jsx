import { Box } from '@mui/material';
import { getTableData4 } from '@components1/k8s/investigate/cards/util';
import CustomTable from '@components1/common/tables/CustomTable2';
import BoxLayout2 from '@components1/common/BoxLayout2';
import Datetime from '@components1/common/format/Datetime';
import { flattenObject } from '@lib/util';

const EdgeDetails = ({ edge }) => {
  const keysToFilter = [
    'cloud_account_id',
    'dest_node_id',
    'source_node_id',
    'level',
    'tenant_id',
    'updated_at',
    'created_at',
    'source',
    'properties',
  ];
  const safeEdge = edge || {};
  const { properties = {}, ...rest } = safeEdge;
  const filtered = Object.fromEntries(Object.entries(rest).filter(([key]) => !keysToFilter.includes(key)));
  const flatProperties = flattenObject(properties);
  const cleanEdges = {
    ...filtered,
    ...flatProperties,
  };
  const { headers = [], convertedJson2 = [] } = getTableData4([cleanEdges]);

  return (
    <BoxLayout2
      heading={'Labels'}
      sharingOptions={{
        download: {
          enabled: false,
          onClick: () => {
            return {
              tableId: '',
            };
          },
        },
        sharing: { enabled: false },
      }}
    >
      <Box sx={{ mb: '12px' }} data-testid='edge-last-seen'>
        <Datetime
          value={safeEdge.updated_at}
          prefix='Last seen: '
          emptyValue='Last seen: -'
          sxPrefix={{ fontFamily: 'Poppins', mr: '4px' }}
          sx={{ fontWeight: 600, color: '#757575', fontFamily: 'Poppins' }}
          sxSuffix={{ fontWeight: 600, color: '#757575', fontFamily: 'Poppins' }}
          sxSecondary={true}
        />
      </Box>

      <CustomTable
        tableData={convertedJson2}
        headers={headers}
        rowsPerPage={convertedJson2.length || 0}
        totalRows={convertedJson2.length || 0}
        onPageChange={undefined}
      />
    </BoxLayout2>
  );
};

export default EdgeDetails;
