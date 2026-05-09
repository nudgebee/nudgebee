import { useRef } from 'react';
import dayjs from 'dayjs';
import { Box, Typography } from '@mui/material';
import Loader from '@common/Loader';
import CustomTooltip from '@common/CustomTooltip';
import { withErrorBoundary } from '@common/ErrorBoundary';

const dayLabels = Array.from({ length: 7 }, (_, index) => {
  return dayjs().subtract(index, 'day').format('D MMM (ddd)');
});
const hourLabels = Array.from({ length: 24 }, (_, index) => `${index.toString().padStart(2, '0')}:00`);

function formatDayAndHour(chartData, dataType) {
  return chartData.reduce((dates, { day, hour, value, cpu, memory, rps }) => {
    const dataValue = dataType === 0 ? cpu : dataType === 1 ? memory : rps;
    (dates[day] = dates[day] || []).push({ hour, value, dataValue });
    return dates;
  }, {});
}

const CustomHeatMap = ({
  data = [],
  xAxisLabels = dayLabels,
  yAxisLabels = hourLabels,
  orientation = 'horizontal',
  customColors = ['#FFFFFF', '#FFF8E1', '#FFF2C7', '#FFF3CA', '#FFEA9F', '#FFDC64'],
  selectedOption,
  showTooltip = true,
  loading = true,
}) => {
  const minMaxCount = useRef([]);
  const formattedData = formatDayAndHour(data, selectedOption);

  const generateBackgroundColor = (count) => {
    return customColors[count];
  };

  const gridCells = xAxisLabels.reduce((days, dayLabel) => {
    const dayAndHour = yAxisLabels.reduce((hours, hourLabel) => {
      const hourData = formattedData[dayLabel]?.find((item) => item.hour === hourLabel) || { value: 0 };
      const formattedHourLabel = dayjs(`${dayLabel} ${hourLabel}`).format('h:mm A');
      const count = hourData.value;
      const dataValue = hourData.dataValue;

      minMaxCount.current = [...minMaxCount.current, count];

      return [
        ...hours,
        {
          dayHour: `${dayLabel} ${formattedHourLabel}`,
          count,
          dataValue,
        },
      ];
    }, []);

    return {
      ...days,
      [dayLabel]: {
        hours: dayAndHour,
      },
    };
  }, {});

  return (
    <div className='container'>
      <div className='container-inner'>
        <div className={`heatmap ${orientation}`}>
          {loading ? (
            <Loader />
          ) : (
            Object.keys(gridCells).map((day) => (
              <div key={day} className='cells col' style={{ gap: '4px', marginBottom: '6px' }}>
                <span className='label first-col'>{day}</span>
                {gridCells[day].hours.map(({ dayHour, count, dataValue }) => (
                  <CustomTooltip
                    key={dayHour}
                    title={
                      showTooltip ? (
                        <Box>
                          <Typography fontSize='20px' fontWeight={600}>
                            {dataValue || '-'}
                          </Typography>
                          <span fontSize='14px'>{dayHour}</span>
                        </Box>
                      ) : undefined
                    }
                    color='#374151'
                  >
                    <div
                      className='cell'
                      style={{
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        backgroundColor: generateBackgroundColor(count),
                        margin: '1px',
                        borderRadius: '2px',
                        width: '24px',
                        height: '24px',
                        padding: '4px',
                      }}
                    >
                      <Typography sx={{ fontSize: '12px' }}>{count.toFixed(0)}</Typography>
                    </div>
                  </CustomTooltip>
                ))}
              </div>
            ))
          )}
          <div className='col'>
            <span className='label first-col' />
            {yAxisLabels.map((label, index) => (
              // Only render every other label text
              <span key={label} className='label'>
                {index % 4 === 0 ? label : null}
              </span>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
};

export default withErrorBoundary(CustomHeatMap);
