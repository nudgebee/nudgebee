import { Box, Typography, Divider } from '@mui/material';
import WidgetCard from '@components1/common/WidgetCard';
import OptimizationSection from './OptimizationSection';
import { colors } from 'src/utils/colors';
import Currency from '@components1/common/format/Currency';
import PropTypes from 'prop-types';
import SafeIcon from '@components1/common/SafeIcon';

const OptimizationCard = ({ sections, sx = {}, cardTitle, cardSavingsValue, cardIcon }) => {
  return (
    <WidgetCard sx={{ marginTop: '12px', ...sx }}>
      {cardTitle && (
        <>
          <Box sx={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
              <SafeIcon src={cardIcon} alt={cardTitle?.toLowerCase() || 'icon'} width={28} height={28} />
              <Typography
                sx={{
                  color: colors.text.secondary,
                  fontSize: '14px',
                  fontWeight: 500,
                  letterSpacing: '-0.2px',
                }}
              >
                {cardTitle}
              </Typography>
            </Box>

            {cardSavingsValue !== undefined && (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                <Currency
                  value={cardSavingsValue}
                  suffix='/mo'
                  sx={{
                    color: colors.text.currency,
                    fontSize: '18px',
                    fontWeight: 500,
                  }}
                />
              </Box>
            )}
          </Box>
          <Divider sx={{ bgcolor: '#EBEBEB', my: 1.5, opacity: 0.35 }} />
        </>
      )}

      <Box sx={{ display: 'flex', flexDirection: 'row', flexWrap: 'wrap', gap: '16px' }}>
        {sections.map((section, index) => (
          <Box
            key={section.id || index}
            sx={{
              flex: '0 0 calc(25% - 12px)',
              maxWidth: 'calc(25% - 12px)',
              minWidth: '200px',
            }}
          >
            <OptimizationSection
              title={section.title}
              savingsValue={section.savingsValue}
              isLoading={section.isLoading}
              tableData={section.tableData}
              tableHeaders={section.tableHeaders}
              viewAllHref={section.viewAllHref}
              rowsPerPage={section.rowsPerPage}
              totalRows={section.totalRows}
            />
          </Box>
        ))}
      </Box>
    </WidgetCard>
  );
};

OptimizationCard.propTypes = {
  sections: PropTypes.arrayOf(
    PropTypes.shape({
      id: PropTypes.string,
      title: PropTypes.string.isRequired,
      savingsValue: PropTypes.number,
      isLoading: PropTypes.bool,
      tableData: PropTypes.array,
      tableHeaders: PropTypes.array.isRequired,
      viewAllHref: PropTypes.string.isRequired,
      rowsPerPage: PropTypes.number,
      totalRows: PropTypes.number,
    })
  ).isRequired,
  sx: PropTypes.object,
  cardTitle: PropTypes.string,
  cardSavingsValue: PropTypes.number,
  cardIcon: PropTypes.object,
};

export default OptimizationCard;
