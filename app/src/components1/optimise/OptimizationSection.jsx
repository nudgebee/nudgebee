import { Box, Typography } from '@mui/material';
import ShimmerLoading from '@components1/common/ShimmerLoading';
import CustomTable from '@components1/common/tables/CustomTable2';
import CustomLink from '@components1/common/CustomLink';
import Currency from '@components1/common/format/Currency';
import { colors } from 'src/utils/colors';
import PropTypes from 'prop-types';

const OptimizationSection = ({ title, savingsValue, isLoading, tableData, tableHeaders, viewAllHref, rowsPerPage = 0, totalRows = 0 }) => {
  return (
    <Box sx={{ width: '100%', height: '100%', display: 'flex', flexDirection: 'column', gap: '8px' }}>
      <ShimmerLoading isLoading={isLoading} width='93%'>
        <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '0px 8px' }}>
          <Typography
            sx={{
              fontFamily: 'Poppins',
              color: colors.text.secondary,
              fontSize: '12px',
              fontWeight: 500,
              letterSpacing: '-0.2px',
            }}
          >
            {title}
          </Typography>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
            <Typography sx={{ color: colors.text.secondaryDark, fontSize: '11px', fontWeight: 400 }}>savings:</Typography>
            <Currency value={savingsValue} suffix='/mo' sx={{ color: colors.text.currency, fontSize: '14px', fontWeight: 500 }} />
          </Box>
        </Box>
        <Box sx={{ backgroundColor: '#F6FAFF', padding: '0px 8px', borderRadius: '8px', height: '100%' }}>
          <CustomTable
            tableData={tableData}
            headers={tableHeaders}
            showUpdatedTable
            showEmptyStateText
            rowsPerPage={rowsPerPage}
            totalRows={totalRows}
            emptyStateText='No Optimisation available'
            hideHeader
            rowBackgroundColor='transparent'
          />
        </Box>
        <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
          <CustomLink secondaryText target='_blank' href={viewAllHref}>
            View All
          </CustomLink>
        </Box>
      </ShimmerLoading>
    </Box>
  );
};

OptimizationSection.propTypes = {
  title: PropTypes.string.isRequired,
  savingsValue: PropTypes.number,
  isLoading: PropTypes.bool,
  tableData: PropTypes.array,
  tableHeaders: PropTypes.array.isRequired,
  viewAllHref: PropTypes.string.isRequired,
  rowsPerPage: PropTypes.number,
  totalRows: PropTypes.number,
};

export default OptimizationSection;
