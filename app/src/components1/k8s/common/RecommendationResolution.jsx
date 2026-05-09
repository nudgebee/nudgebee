import { useEffect, useState } from 'react';
import PropTypes from 'prop-types';
import recommendationApi from '@api1/recommendation';
import BoxLayout2 from '@components1/common/BoxLayout2';
import CustomTable from '@components1/common/tables/CustomTable2';
import Datetime from '@components1/common/format/Datetime';
import Link from 'next/link';
import { containsLink } from 'src/utils/common';
import { Typography } from '@mui/material';
import { colors } from 'src/utils/colors';
import apiUser from '@api1/user';

const RecommendationResolutionRequest = function (accountId, drilldownQuery, _row) {
  let data = drilldownQuery?.resolution?.data?.data;
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
  return <pre>{data}</pre>;
};

const RecommendationResolutionStatusDetails = function (_accountId, drilldownQuery, _row) {
  const statusMessage = drilldownQuery?.resolution?.status_message;
  if (!statusMessage) {
    return <Typography sx={{ fontSize: '14px', color: colors.text.secondary }}>No details available</Typography>;
  }
  return <pre style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>{statusMessage}</pre>;
};

const truncateToFirstLine = (text, maxLength = 80) => {
  if (!text) {
    return '-';
  }
  const firstLine = text.split('\n')[0];
  if (firstLine.length <= maxLength) {
    return firstLine;
  }
  return firstLine.substring(0, maxLength) + '...';
};

const RecommendationResolution = ({ recommendation }) => {
  const [resolutions, setResolutions] = useState([]);
  const [tableData, setTableData] = useState([]);
  const [loading, setLoading] = useState(false);

  const [perPage, setPerPage] = useState(apiUser.getUserPreferencesTablePageSize ?? 10);
  const [currentPage, setCurrentPage] = useState(1);

  const [totalRows, setTotalRows] = useState(0);

  useEffect(() => {
    if (!recommendation?.id) {
      return;
    }
    setLoading(true);
    recommendationApi.listRecommendationResolution(recommendation.id, perPage, (currentPage - 1) * perPage).then(async (res) => {
      setResolutions(res?.data?.recommendation_resolution);

      // Get unique resolver_ids first
      const uniqueResolverIds = [
        ...new Set(res?.data?.recommendation_resolution.filter((resolution) => resolution?.resolver_id).map((resolution) => resolution.resolver_id)),
      ];
      // Fetch user data for unique IDs only
      const usersMap = {};
      await Promise.all(
        uniqueResolverIds.map(async (resolverId) => {
          try {
            const userResponse = await apiUser.getUser(resolverId);
            const user = userResponse?.data;
            if (user) {
              usersMap[resolverId] = user.display_name || user.firstname || user.username;
            }
          } catch (error) {
            console.error(error);
          }
        })
      );
      let tableData = res?.data?.recommendation_resolution.map((resolution) => {
        const referenceObj = {};
        if (containsLink(resolution?.type_reference_id)) {
          referenceObj['component'] = (
            <Link
              onClick={(e) => e.stopPropagation()}
              href={resolution?.type_reference_id}
              target='_blank'
              style={{ fontSize: '13px', fontWeight: 400 }}
            >
              {resolution?.type_reference_id}
            </Link>
          );
        } else {
          referenceObj['text'] = (
            <Typography sx={{ fontSize: '13px', fontWeight: 400, color: colors.text.secondary }}>{resolution?.type_reference_id}</Typography>
          );
        }

        const resolverDisplay =
          resolution?.resolver_id && usersMap[resolution.resolver_id] ? usersMap[resolution.resolver_id] : resolution?.resolver_type || '-';

        return [
          {
            text: <Typography sx={{ fontSize: '14px', fontWeight: 400, color: colors.text.secondary }}>{resolution?.type}</Typography>,
            drilldownQuery: { resolution: resolution },
          },
          referenceObj,
          {
            text: <Typography sx={{ fontSize: '13px', fontWeight: 400, color: colors.text.secondary }}>{resolverDisplay}</Typography>,
          },
          {
            text: <Typography sx={{ fontSize: '13px', fontWeight: 400, color: colors.text.secondary }}>{resolution?.status}</Typography>,
          },
          {
            text: (
              <Typography sx={{ fontSize: '13px', fontWeight: 400, color: colors.text.secondary }}>
                {truncateToFirstLine(resolution?.status_message)}
              </Typography>
            ),
          },
          {
            component: <Datetime value={resolution?.updated_at} />,
          },
        ];
      });
      setTotalRows(res?.data?.recommendation_resolution_aggregate.aggregate.count);
      setTableData(tableData);
      setLoading(false);
    });
  }, [recommendation?.id, currentPage, perPage]);

  return resolutions?.length >= 0 ? (
    <BoxLayout2
      sharingOptions={{
        download: {
          enabled: false,
        },
        sharing: {
          enabled: false,
        },
      }}
    >
      <CustomTable
        headers={['Type', 'Reference', 'Resolver', 'Status', 'Status Message', 'Updated At']}
        tableData={tableData}
        pageNumber={currentPage}
        rowsPerPage={perPage}
        totalRows={totalRows}
        expandable={{
          tabs: [
            {
              componentFn: RecommendationResolutionRequest,
              text: 'Request',
            },
            {
              componentFn: RecommendationResolutionStatusDetails,
              text: 'Status Details',
            },
          ],
        }}
        loading={loading}
        onPageChange={(page, limit) => {
          setCurrentPage(page);
          setPerPage(limit);
        }}
      />
    </BoxLayout2>
  ) : (
    <div>No Resolution Found</div>
  );
};

RecommendationResolution.propTypes = {
  recommendation: PropTypes.any,
};

export default RecommendationResolution;
