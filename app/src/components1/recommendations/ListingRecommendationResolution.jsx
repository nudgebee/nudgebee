import apiRecommendations from '@api1/recommendation';
import apiUser from '@api1/user';
import { BoxLayout2 } from '@components1/common';
import Currency from '@components1/common/format/Currency';
import Datetime from '@components1/common/format/Datetime';
import Text from '@components1/common/format/Text';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import { Box, Typography } from '@mui/material';
import Link from 'next/link';
import PropTypes from 'prop-types';
import { useEffect, useState } from 'react';
import { colors } from 'src/utils/colors';
import { containsLink, snakeToTitleCase } from 'src/utils/common';
import useCurrencySymbol from '@hooks/useCurrencySymbol';

const ListingRecommendationResolution = ({ accountId }) => {
  const recommendationResolutionTableId = 'recommendation-resolution';

  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [selectedStatus, setSelectedStatus] = useState('InProgress');
  const [recommendationTypes, setRecommendationTypes] = useState([]);
  const [resolverTypes, setResolverTypes] = useState([]);
  const [selectedRecommendation, setSelectedRecommendation] = useState('');
  const [selectedResolver, setSelectedResolver] = useState('');
  const currencySymbol = useCurrencySymbol(accountId);

  useEffect(() => {
    apiRecommendations.getDistinctResolverTypes('type').then((res) => {
      setRecommendationTypes(
        res?.data?.data?.recommendation_resolution.map((item) => {
          return item.type;
        })
      );
    });
    apiRecommendations.getDistinctResolverTypes('resolver_type').then((res) => {
      setResolverTypes(
        res?.data?.data?.recommendation_resolution.map((item) => {
          return item.resolver_type;
        })
      );
    });
  }, []);

  const getResolutionListingData = () => {
    setData([]);
    setLoading(true);
    apiRecommendations
      .getRecommendationResolution({
        limit: rowsPerPage,
        offset: rowsPerPage * page,
        accountId: accountId,
        status: selectedStatus,
        type: selectedRecommendation,
        resolverType: selectedResolver,
      })
      .then((res) => {
        const resolutionData = res?.data?.data?.recommendation_resolution || [];
        const resolutionCount = res?.data?.data?.recommendation_resolution_aggregate?.aggregate?.count || 0;

        if (resolutionData.length > 0) {
          const data = resolutionData.map((rr) => {
            const namespace =
              rr.recommendation.cloud_resourse?.meta?.namespace ||
              rr.recommendation.cloud_resourse?.meta?.config?.namespace ||
              rr.recommendation.recommendation?.spec?.claimRef?.namespace ||
              rr.recommendation.recommendation?.metadata?.namespace ||
              rr.recommendation.recommendation?.namespace ||
              '';
            const name = rr?.recommendation?.recommendation?.spec?.claimRef?.name || rr?.recommendation?.recommendation?.metadata?.name || '';
            const workloadName =
              rr.recommendation?.cloud_resourse?.meta?.controller ||
              rr.recommendation?.cloud_resourse?.meta?.config?.labels?.['app.kubernetes.io/name'];

            const referenceObj = {};
            if (containsLink(rr.type_reference_id)) {
              referenceObj['component'] = (
                <Link onClick={(e) => e.stopPropagation()} href={rr?.type_reference_id} target='_blank' style={{ fontSize: '14px', fontWeight: 400 }}>
                  {rr.type}
                </Link>
              );
            } else {
              referenceObj['text'] = <Typography sx={{ fontSize: '13px', fontWeight: 400, color: colors.text.secondary }}>{rr.type}</Typography>;
            }

            return [
              {
                component: (
                  <Box display='flex' flexDirection='column'>
                    {rr.recommendation.rule_name && (
                      <Box sx={{ display: 'flex' }}>
                        <Typography sx={{ fontSize: '13px', fontWeight: 500, color: '#374151', mr: '4px' }}>Rec: </Typography>
                        <Typography sx={{ fontWeight: 400, fontSize: '13px' }}>{snakeToTitleCase(rr.recommendation.rule_name)}</Typography>
                      </Box>
                    )}
                    {rr.recommendation.severity && (
                      <Box sx={{ display: 'flex' }}>
                        <Typography sx={{ fontSize: '13px', fontWeight: 500, mr: '4px' }}>Severity:</Typography>
                        <Typography sx={{ fontWeight: 400, fontSize: '13px', alignContent: 'center', marginTop: '3px' }}>
                          <CustomLabels height='12px' text={rr.recommendation.severity} />
                        </Typography>
                      </Box>
                    )}
                    {name && (
                      <Box sx={{ display: 'flex' }}>
                        <Typography sx={{ fontSize: '14px', fontWeight: 500, color: '#374151', mr: '4px' }}>Name:</Typography>
                        <Text value={name} showAutoEllipsis={true} sx={{ maxWidth: '120px', fontSize: '14px', fontWeight: 400 }} />
                      </Box>
                    )}
                    {namespace && (
                      <Box sx={{ display: 'flex' }}>
                        <Typography sx={{ fontSize: '13px', fontWeight: 500, color: '#374151', mr: '4px' }}>Namespace:</Typography>
                        <Text value={namespace} showAutoEllipsis={true} sx={{ maxWidth: '120px', fontSize: '13px', fontWeight: 400 }} />
                      </Box>
                    )}
                    {workloadName && (
                      <Box sx={{ display: 'flex' }}>
                        <Typography sx={{ fontSize: '13px', fontWeight: 500, color: '#374151', mr: '4px' }}>Workload:</Typography>
                        <Text value={workloadName} showAutoEllipsis={true} sx={{ maxWidth: '120px', fontSize: '13px', fontWeight: 400 }} />
                      </Box>
                    )}
                  </Box>
                ),
                drilldownQuery: {
                  recommendation: rr,
                  message: rr.status_message,
                },
              },
              {
                component: <CustomLabels text={rr.status === 'InProgress' ? 'In Progress' : rr.status} />,
              },
              {
                text: (
                  <Currency
                    precison={1}
                    value={rr.recommendation.estimated_savings}
                    prefix={currencySymbol || '$'}
                    varient='savings'
                    sx={{
                      fontWeight: 500,
                      fontSize: '13px',
                      color: '#374151',
                    }}
                    sxPrefix={{
                      fontSize: '12px',
                      fontWeight: 400,
                      color: '#9F9F9F',
                    }}
                  />
                ),
              },
              {
                component: (() => {
                  const resolverName = rr.resolver_display_name || rr.data?.provider_config?.name;
                  return (
                    <Box display='flex' flexDirection='column'>
                      <Typography sx={{ fontSize: '13px', fontWeight: 400, color: colors.text.secondary }}>
                        {rr.resolver_type ? rr.resolver_type : '-'}
                      </Typography>
                      {resolverName && (
                        <Typography sx={{ fontSize: '12px', fontWeight: 400, color: colors.text.secondary }}>{resolverName}</Typography>
                      )}
                    </Box>
                  );
                })(),
              },
              referenceObj,
              {
                component: <Datetime value={rr.updated_at} />,
              },
            ];
          });
          setData(data);
        }
        setTotalCount(resolutionCount);
      })
      .finally(() => {
        setLoading(false);
      });
  };
  useEffect(() => {
    if (currencySymbol === undefined) return;
    getResolutionListingData();
  }, [accountId, selectedStatus, rowsPerPage, page, selectedRecommendation, selectedResolver, currencySymbol]);

  return (
    <BoxLayout2
      id='box-layout-recommendation-resolution'
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: recommendationResolutionTableId,
            };
          },
        },
        sharing: { enabled: false },
      }}
      filterOptions={[
        {
          type: 'dropdown',
          enabled: true,
          options: [
            { label: 'Success', value: 'Success' },
            { label: 'Failed', value: 'Failed' },
            { label: 'In Progress', value: 'InProgress' },
          ],
          onSelect: (e) => {
            setSelectedStatus(e.target.value);
            setPage(0);
          },
          value: selectedStatus,
          minWidth: '150px',
          label: 'Status',
        },
        {
          type: 'dropdown',
          enabled: true,
          options: recommendationTypes,
          onSelect: (e) => {
            setSelectedRecommendation(e.target.value);
            setPage(0);
          },
          value: selectedRecommendation,
          minWidth: '150px',
          label: 'Recommendation',
        },
        {
          type: 'dropdown',
          enabled: true,
          options: resolverTypes,
          onSelect: (e) => {
            setSelectedResolver(e.target.value);
            setPage(0);
          },
          value: selectedResolver,
          minWidth: '150px',
          label: 'Resolver',
        },
      ]}
    >
      <KubernetesTable2
        id={recommendationResolutionTableId}
        headers={[
          { name: 'Recommendation', width: '20%' },
          { name: 'Status', width: '10%' },
          { name: 'Est. Savings', width: '10%' },
          { name: 'Resolver', width: '20%' },
          { name: 'Type', width: '20%' },
          { name: 'Updated At', width: '10%' },
        ]}
        data={data}
        onPageChange={(page, limit) => {
          setPage(page - 1);
          setRowsPerPage(limit);
        }}
        loading={loading}
        rowsPerPage={rowsPerPage}
        showExpandable={true}
        totalRows={totalCount}
        expandable={{
          tabs: [
            {
              componentFn: function (accountId, drilldownQuery) {
                let data = drilldownQuery.recommendation.data?.data;
                if (data && typeof data === 'string') {
                  try {
                    data = JSON.parse(data);
                    data = JSON.stringify(data, null, 2);
                  } catch (e) {
                    console.error(e);
                  }
                } else {
                  data = JSON.stringify(data, null, 2);
                }
                if (!data) {
                  data = `No Details Available`;
                }
                return (
                  <pre
                    style={{
                      backgroundColor: '#ffffff',
                      padding: '16px',
                      borderRadius: '4px',
                      whiteSpace: 'pre-wrap',
                      wordWrap: 'break-word',
                      overflowWrap: 'break-word',
                      maxWidth: '100%',
                      overflow: 'auto',
                    }}
                  >
                    {data}
                  </pre>
                );
              },
              text: 'Details',
            },
            {
              componentFn: function (accountId, drilldownQuery) {
                let data = drilldownQuery.message;

                if (!data) {
                  data = `No Message Available`;
                }
                return (
                  <pre
                    style={{
                      backgroundColor: '#ffffff',
                      padding: '16px',
                      borderRadius: '4px',
                      whiteSpace: 'pre-wrap',
                      wordWrap: 'break-word',
                      overflowWrap: 'break-word',
                      maxWidth: '100%',
                      overflow: 'auto',
                    }}
                  >
                    {data}
                  </pre>
                );
              },
              text: 'Message',
            },
          ],
        }}
        pageNumber={page + 1}
      />
    </BoxLayout2>
  );
};

ListingRecommendationResolution.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default ListingRecommendationResolution;
