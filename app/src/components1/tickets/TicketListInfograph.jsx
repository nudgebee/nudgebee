import React, { useState, useEffect } from 'react';
import { Box, Typography } from '@mui/material';
import DoughnutChart from '@components1/common/charts/DoughnutChart';
import ticketApi from '@api1/tickets';
import ColorDots from '@components1/common/widgets/ColorDots';
import PropTypes from 'prop-types';
import { colors } from 'src/utils/colors';

const TitleWithValue = ({ dots = false, title, value = 0, displaySign = false, textAlign, customSign, onClick, active = false }) => {
  return (
    <Box
      onClick={onClick ? () => onClick(title) : undefined}
      sx={{
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'center',
        height: '100%',
        gap: '4px',
        ...(onClick && {
          cursor: 'pointer',
          borderRadius: '6px',
          px: 0.5,
          '&:hover': { backgroundColor: colors.background.tertiaryLightestest },
        }),
      }}
    >
      <Box
        sx={{
          display: 'flex',
          flexDirection: 'block',
          alignItems: 'center',
          gap: '15px',
          '@media (max-width: 1500px)': {
            gap: '7px',
            p: {
              fontSize: '14px',
            },
            '& .dotTitle': {
              fontSize: '10px',
            },
          },
        }}
      >
        {dots && <ColorDots severity={title} active={active} />}
        <Box>
          <Typography
            textAlign={textAlign}
            color={active ? colors.text.secondary : colors.text.secondaryDark}
            fontSize={'12px'}
            mb={'2px'}
            lineHeight={'18px'}
            fontWeight={active ? 600 : 400}
            className='dotTitle'
          >
            {title}
          </Typography>
          <Typography
            textAlign={textAlign}
            color={colors.text.secondary}
            fontSize={'20px'}
            lineHeight={'23.4px'}
            fontWeight={active ? 700 : 500}
            sx={{
              '@media (max-width: 1500px)': {
                gap: '7px',
                p: {
                  fontSize: '14px',
                },
              },
            }}
          >
            {displaySign ? '$' : ''}
            {value?.toLocaleString()}
            {!!customSign && <span style={{ fontSize: '18px', color: colors.text.secondaryDark }}> {customSign}</span>}
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
          borderColor: colors.border.white,
          backgroundColor: colors.background.white,
          boxShadow: '0px 2px 12px 2px #00000014',
          padding: '20px 24px',
          borderRadius: '8px',
          marginTop: '0px',
          height: '100%',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: '20px',
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
    { count: 0, severity: 'Highest', color: colors.highest },
    { count: 0, severity: 'High', color: colors.high },
    { count: 0, severity: 'Medium', color: colors.medium },
    { count: 0, severity: 'Low', color: colors.low },
    { count: 0, severity: 'Lowest', color: colors.lowest },
    { count: 0, severity: 'NA', color: colors.NA },
  ];
  const TotalTiecketDataArray = [
    { count: 0, status: 'To Do', color: colors.toDo },
    { count: 0, status: 'Done', color: colors.done },
    { count: 0, status: 'In Progress', color: colors.inProgress },
    { count: 0, status: 'open', color: colors.open },
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
    <Box sx={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '10px' }}>
      <SummaryBlock
        sx={{
          '@media (max-width: 1500px)': {
            padding: '10px',
            gap: '5px',
          },
        }}
      >
        <Box
          display='flex'
          alignItems='center'
          gap='10px'
          sx={{
            '@media (max-width: 1500px)': {
              '& p': {
                fontSize: '10px',
                lineHeight: '0px',
              },
              '& span': {
                fontSize: '12px',
              },
              '& #doughnutChart': {
                height: '40px !important',
                width: '40px !important',
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
            <Typography variant='span' sx={{ color: colors.text.secondary, fontSize: '16px', fontWeight: '500' }}>
              Total <br /> Tickets
            </Typography>
            <Box sx={{ display: 'flex', gap: '8px', mt: 0.5 }}>
              <Typography sx={{ fontSize: '11px', color: colors.text.primary, fontWeight: 500 }}>{openCount} Open</Typography>
              <Typography sx={{ fontSize: '11px', color: colors.success, fontWeight: 500 }}>{closedCount} Closed</Typography>
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
            padding: '10px',
            gap: '5px',
          },
        }}
      >
        <Box
          display='flex'
          alignItems='center'
          gap='10px'
          sx={{
            '@media (max-width: 1500px)': {
              '& p': {
                fontSize: '10px',
                lineHeight: '0px',
              },
              '& span': {
                fontSize: '12px',
              },
              '& #doughnutChart': {
                height: '40px !important',
                width: '40px !important',
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
          <Typography variant='span' sx={{ color: colors.text.secondary, fontSize: '16px', fontWeight: '500' }}>
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
