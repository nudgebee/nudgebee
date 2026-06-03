import React, { useState, useEffect } from 'react';
import { Box, Typography } from '@mui/material';
import DoughnutChart from '@components1/common/charts/DoughnutChart';
import ticketApi from '@api1/tickets';
import ColorDots from '@components1/common/widgets/ColorDots';
import PropTypes from 'prop-types';
import { ds } from 'src/utils/colors';

const TitleWithValue = ({ dots = false, title, value = 0, displaySign = false, textAlign, customSign, onClick, active = false }) => {
  return (
    <Box
      onClick={onClick ? () => onClick(title) : undefined}
      sx={{
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        height: '100%',
        gap: 'var(--ds-space-1)',
        ...(onClick && {
          cursor: 'pointer',
          borderRadius: 'var(--ds-radius-md)',
          px: ds.space[1],
          '&:hover': { backgroundColor: ds.background[200] },
        }),
      }}
    >
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'block',
          alignItems: 'center',
          gap: 'var(--ds-space-4)',
          '@media (max-width: 1500px)': {
            gap: 'var(--ds-space-2)',
            p: {
              fontSize: 'var(--ds-text-body-lg)',
            },
            '& .dotTitle': {
              fontSize: 'var(--ds-text-caption)',
            },
          },
        }}
      >
        {dots && <ColorDots severity={title} active={active} />}
        <Box>
          <Typography
            textAlign={textAlign}
            color={active ? ds.gray[700] : ds.gray[400]}
            fontSize={ds.text.small}
            mb={ds.space[0]}
            lineHeight={'18px'}
            fontWeight={active ? 600 : 400}
            className='dotTitle'
          >
            {title}
          </Typography>
          <Typography
            textAlign={textAlign}
            color={ds.gray[700]}
            fontSize={ds.text.heading}
            lineHeight={'23.4px'}
            fontWeight={active ? 700 : 500}
            sx={{
              '@media (max-width: 1500px)': {
                gap: 'var(--ds-space-2)',
                p: {
                  fontSize: 'var(--ds-text-body-lg)',
                },
              },
            }}
          >
            {displaySign ? '$' : ''}
            {value?.toLocaleString()}
            {!!customSign && <span style={{ fontSize: 'var(--ds-text-title)', color: ds.gray[400] }}> {customSign}</span>}
          </Typography>
        </Box>
      </Box>
    </Box>
  );
};

TitleWithValue.propTypes = {
  dots: PropTypes.bool,
  title: PropTypes.string,
  value: PropTypes.any,
  displaySign: PropTypes.bool,
  textAlign: PropTypes.string,
  customSign: PropTypes.string,
  onClick: PropTypes.func,
  active: PropTypes.bool,
};

const SummaryBlock = ({ children, sx }) => {
  return (
    <Box display='flex' flexDirection='column' justifyContent='space-between'>
      <Box
        sx={{
          border: '1px solid',
          borderColor: ds.background[100],
          backgroundColor: ds.background[100],
          boxShadow: '0px 2px 12px 2px #00000014',
          padding: 'var(--ds-space-4) var(--ds-space-5)',
          borderRadius: 'var(--ds-radius-lg)',
          marginTop: '0px',
          height: '100%',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 'var(--ds-space-4)',
          ...sx,
        }}
      >
        {children}
      </Box>
    </Box>
  );
};

SummaryBlock.propTypes = {
  children: PropTypes.any,
  sx: PropTypes.any,
};

const customSeverityOrder = ['Highest', 'High', 'Medium', 'Low', 'Lowest', 'NA'];
const CLOSED_STATUSES = ['Done', 'Closed', 'Resolved'];

const TicketListInfoGraph = ({ defaultQuery = {}, selectedStatus, selectedPriority, setSelectedStatus, setSelectedPriority }) => {
  const [data, setData] = useState({ status_groupings: [], severity_groupings: [] });

  useEffect(() => {
    ticketApi.getSummary(defaultQuery).then((res) => {
      setData(res.data);
    });
  }, [defaultQuery?.assignee, defaultQuery?.tool, defaultQuery?.account_id]);

  const handleStatusClick = (status) => {
    setSelectedStatus(selectedStatus === status ? null : status);
  };

  const handlePriorityClick = (priority) => {
    setSelectedPriority(selectedPriority === priority ? null : priority);
  };

  const sortedPriorities = [...(data?.severity_groupings || [])]
    ?.sort((a, b) => customSeverityOrder.indexOf(a.severity) - customSeverityOrder.indexOf(b.severity))
    .map((item) => {
      if (item.severity === null) {
        return { ...item, severity: 'Critical' };
      }
      return item;
    });

  const PriorityDataArray = [
    { count: 0, severity: 'Highest', color: ds.red[500] },
    { count: 0, severity: 'High', color: ds.red[500] },
    { count: 0, severity: 'Medium', color: ds.amber[400] },
    { count: 0, severity: 'Low', color: ds.blue[500] },
    { count: 0, severity: 'Lowest', color: ds.green[500] },
    { count: 0, severity: 'NA', color: ds.gray[400] },
  ];
  const TotalTiecketDataArray = [
    { count: 0, status: 'To Do', color: ds.blue[300] },
    { count: 0, status: 'Done', color: ds.green[300] },
    { count: 0, status: 'In Progress', color: ds.yellow[400] },
    { count: 0, status: 'open', color: ds.purple[300] },
  ];
  const updateDataArray = (dataArray, matchingArray, key) => {
    return dataArray.map((dataItem) => {
      const matchingItem = matchingArray.find((item) => item[key] === dataItem[key]);
      return matchingItem ? { ...dataItem, count: matchingItem.count } : dataItem;
    });
  };

  const updatedPriorityData = updateDataArray(PriorityDataArray, sortedPriorities, 'severity');
  const updatedTotalTicketData = updateDataArray(TotalTiecketDataArray, data?.status_groupings || [], 'status');

  PriorityDataArray.forEach((priority) => {
    if (!updatedPriorityData.some((item) => item.severity === priority.severity)) {
      updatedPriorityData.push(priority);
    }
  });

  updatedPriorityData.sort(
    (a, b) =>
      PriorityDataArray.findIndex((item) => item.severity === a.severity) - PriorityDataArray.findIndex((item) => item.severity === b.severity)
  );

  // Compute open vs closed counts
  const closedCount = (data?.status_groupings || []).filter((s) => CLOSED_STATUSES.includes(s.status)).reduce((sum, s) => sum + (s.count || 0), 0);
  const openCount = (data?.total_count || 0) - closedCount;

  return (
    <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 'var(--ds-space-2)' }}>
      <SummaryBlock
        sx={{
          '@media (max-width: 1500px)': {
            padding: 'var(--ds-space-2)',
            gap: 'var(--ds-space-1)',
          },
        }}
      >
        <Box
          display='flex'
          alignItems='center'
          gap={ds.space.mul(0, 5)}
          sx={{
            '@media (max-width: 1500px)': {
              '& p': {
                fontSize: 'var(--ds-text-caption)',
                lineHeight: '0px',
              },
              '& span': {
                fontSize: 'var(--ds-text-small)',
              },
              '& #doughnutChart': {
                height: `${ds.space.mul(0, 20)} !important`,
                width: `${ds.space.mul(0, 20)} !important`,
              },
            },
          }}
        >
          <DoughnutChart
            size={'60px'}
            borderWidth={0}
            borderRadius={0}
            values={updatedTotalTicketData?.map((s) => (typeof s?.count === 'number' ? s?.count : 0))}
            labels={updatedTotalTicketData?.map((s) => s?.status)}
            displayValue={data?.total_count || 0}
            valueUnit=''
            colors={updatedTotalTicketData?.map((s) => s?.color)}
            enableTooltip
            displayOnlyValueOnTooltip
            onItemClick={handleStatusClick}
          />
          <Box display='flex' flexDirection='column'>
            <Typography variant='span' sx={{ color: ds.gray[700], fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-medium)' }}>
              Total <br /> Tickets
            </Typography>
            <Box sx={{ display: 'flex', gap: 'var(--ds-space-2)', mt: ds.space[1] }}>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.blue[500], fontWeight: 'var(--ds-font-weight-medium)' }}>
                {openCount} Open
              </Typography>
              <Typography sx={{ fontSize: 'var(--ds-text-caption)', color: ds.green[600], fontWeight: 'var(--ds-font-weight-medium)' }}>
                {closedCount} Closed
              </Typography>
            </Box>
          </Box>
        </Box>
        {updatedTotalTicketData?.map((s) => (
          <TitleWithValue dots key={s?.status} title={s?.status} value={s?.count} onClick={handleStatusClick} active={selectedStatus === s?.status} />
        ))}
      </SummaryBlock>

      <SummaryBlock
        sx={{
          '@media (max-width: 1500px)': {
            padding: 'var(--ds-space-2)',
            gap: 'var(--ds-space-1)',
          },
        }}
      >
        <Box
          display='flex'
          alignItems='center'
          gap={ds.space.mul(0, 5)}
          sx={{
            '@media (max-width: 1500px)': {
              '& p': {
                fontSize: 'var(--ds-text-caption)',
                lineHeight: '0px',
              },
              '& span': {
                fontSize: 'var(--ds-text-small)',
              },
              '& #doughnutChart': {
                height: `${ds.space.mul(0, 20)} !important`,
                width: `${ds.space.mul(0, 20)} !important`,
              },
            },
          }}
        >
          <DoughnutChart
            borderWidth={0}
            borderRadius={0}
            size={'60px'}
            values={updatedPriorityData?.map((s) => (typeof s?.count === 'number' ? s?.count : 0))}
            labels={updatedPriorityData?.map((s) => s?.severity)}
            displayValue={data?.total_count || 0}
            valueUnit=''
            colors={updatedPriorityData?.map((s) => s?.color)}
            enableTooltip
            displayOnlyValueOnTooltip
            onItemClick={handlePriorityClick}
          />
          <Typography variant='span' sx={{ color: ds.gray[700], fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-medium)' }}>
            Priority
          </Typography>
        </Box>
        {updatedPriorityData?.map((s) => (
          <TitleWithValue
            dots
            key={s?.severity}
            title={s?.severity}
            value={s?.count}
            onClick={handlePriorityClick}
            active={selectedPriority === s?.severity}
          />
        ))}
      </SummaryBlock>
    </Box>
  );
};

export default TicketListInfoGraph;

TicketListInfoGraph.propTypes = {
  defaultQuery: PropTypes.object,
  selectedStatus: PropTypes.string,
  selectedPriority: PropTypes.string,
  setSelectedStatus: PropTypes.func,
  setSelectedPriority: PropTypes.func,
};
