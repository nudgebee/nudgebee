import { Box } from '@mui/material';
import { getTableData4 } from '@components/k8s/investigate/cards/util';
import CustomTable from '@shared/tables/CustomTable2';
import { ListingLayout } from '@ui/ListingLayout';
import Datetime from '@shared/format/Datetime';
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
  const { properties, source_name, destination_name, ...rest } = safeEdge;
  // Where the edge came from: every collector that asserted it (`contributing_sources`,
  // e.g. ["aws"] or ["manual"]), else the winning top-level `source`. Shown as one
  // "Detected By" row so it isn't confused with the From node or `properties.source`.
  const { contributing_sources: contributingSources, source: derivedFrom, ...otherProperties } = properties || {};
  const origins = Array.isArray(contributingSources) && contributingSources.length ? contributingSources : [safeEdge.source];
  const filtered = Object.fromEntries(Object.entries(rest).filter(([key]) => !keysToFilter.includes(key)));
  const flatProperties = flattenObject(otherProperties);
  const cleanEdges = {
    ...filtered,
    from: source_name,
    to: destination_name,
    detected_by: origins.filter(Boolean).join(', ') || '-',
    ...(derivedFrom !== undefined && { derived_from: derivedFrom }),
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
