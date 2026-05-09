import apiBilling from '@api1/billing';
import { BoxLayout2 } from '@components1/common';
import CustomTable from '@components1/common/tables/CustomTable2';
import dayjs from 'dayjs';
import { useEffect, useState } from 'react';
import { Box, Typography } from '@mui/material';
import Currency from '@components1/common/format/Currency';
import { SummaryBlock } from '@components1/k8s/KubernetesClusterSummary';
import MonthlyCostSummary from './MonthlyCostSummary';
import { colors } from 'src/utils/colors';
import apiUser from '@api1/user';

interface InforgraphicsProps {
  totalDue?: number;
  totalBilled?: number;
  totalPaid?: number;
}
const Billing = () => {
  const [tableData, setTableData] = useState([]);
  const [infographicsData, setInfographicsData] = useState<InforgraphicsProps>({});
  const [loading, setLoading] = useState(false);
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [currentPage, setCurrentPage] = useState(0);
  const [totalCount, setTotalCount] = useState(0);

  useEffect(() => {
    apiBilling.getBillingsInfographics().then((res) => {
      setInfographicsData({
        totalDue: res?.data?.total_amount_due?.aggregate?.sum?.amount_due,
        totalBilled: res?.data?.total_billed_amount?.aggregate?.sum?.last_billed_amount,
        totalPaid: res?.data?.total_amount_due?.aggregate?.sum?.total_paid,
      });
    });
  }, []);

  useEffect(() => {
    setLoading(true);
    setTableData([]);
    apiBilling
      .getBillingsListing(recordsPerPage, currentPage * recordsPerPage)
      .then((res) => {
        const rows = res?.data?.billing?.map((item: any) => {
          return [
            { text: dayjs(item?.last_billed_date).format('MMMM - YYYY'), drilldownQuery: { date: item?.last_billed_date } },
            {
              text: item?.last_billed_amount,
            },
            {
              text: item?.amount_due,
            },
          ];
        });
        setTableData(rows);
        setTotalCount(res?.data?.total_count?.aggregate?.count || 0);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [currentPage, recordsPerPage]);

  const onPageChange = (page: number, limit: number) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  return (
    <BoxLayout2
      id={'billing-box'}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: 'billing-index',
            };
          },
        },
        sharing: { enabled: true, onClick: null },
      }}
    >
      <Box sx={{ mt: '8px', display: 'flex', justifyContent: 'flex-start' }}>
        <></>
        <SummaryBlock
          hideTitle
          sx={{
            borderRadius: '4px',
            minHeight: '50px',
            backgroundColor: `${colors.background.white} !important`,
            border: `0.5px solid ${colors.border.success} !important`,
            boxShadow: '0px 4px 6px -1px #E5E5E599',
            '@media (max-width: 1350px)': {
              padding: '16px 10px',
            },
          }}
        >
          <Box display={'flex'} gap='32px' justifyContent={'space-between'}>
            <Box>
              <Typography sx={{ fontSize: '16px', fontWeight: 400, color: colors.text.secondaryDark }}>Total Amount Billed</Typography>
              <Typography variant='h4' sx={{ fontSize: '16px', fontWeight: 500, color: colors.text.secondary }}>
                <Currency
                  value={infographicsData?.totalBilled}
                  sx={{
                    fontWeight: 500,
                    fontSize: '24px',
                    color: colors.text.secondary,
                  }}
                />
              </Typography>
            </Box>
            <Box>
              <Typography sx={{ fontSize: '16px', fontWeight: 400, color: colors.text.secondaryDark }}>Total Due</Typography>
              <Typography variant='h4' sx={{ fontSize: '16px', fontWeight: 500, color: colors.text.secondary }}>
                <Currency
                  value={infographicsData.totalDue}
                  sx={{
                    fontWeight: 500,
                    fontSize: '24px',
                    color: colors.text.secondary,
                  }}
                />
              </Typography>
            </Box>
          </Box>
        </SummaryBlock>
      </Box>
      <CustomTable
        tableData={tableData}
        id={'billing-index'}
        headers={['Bill Month', 'Billed Amount in USD', 'Amount Due in USD']}
        showExpandable={true}
        expandable={{
          tabs: [
            {
              text: 'Details',
              value: 0,
              componentFn: function (opt: any, drilldownQuery: any) {
                return <MonthlyCostSummary query={drilldownQuery} />;
              },
            },
          ],
        }}
        loading={loading}
        rowsPerPage={recordsPerPage}
        onPageChange={onPageChange}
        pageNumber={currentPage + 1}
        totalRows={totalCount}
      />
    </BoxLayout2>
  );
};

export default Billing;
