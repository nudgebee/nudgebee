import { Box } from '@mui/material';
import { getTableData4 } from '@components1/k8s/investigate/cards/util';
import CustomTable from '@common-new/tables/CustomTable2';
import { ListingLayout } from '@components1/ds/ListingLayout';
import Datetime from '@common-new/format/Datetime';
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
    <ListingLayout id='edge-details-labels'>
      <ListingLayout.Toolbar title='Labels' />
      <ListingLayout.Body>
        <Box sx={{ mb: 'var(--ds-space-3)' }} data-testid='edge-last-seen'>
          <Datetime
            value={safeEdge.updated_at}
            prefix='Last seen: '
            emptyValue='Last seen: -'
            sxPrefix={{ fontFamily: 'var(--ds-font-display)', mr: 'var(--ds-space-1)' }}
            sx={{ fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-500)', fontFamily: 'var(--ds-font-display)' }}
            sxSuffix={{ fontWeight: 'var(--ds-font-weight-semibold)', color: 'var(--ds-gray-500)', fontFamily: 'var(--ds-font-display)' }}
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
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default EdgeDetails;
