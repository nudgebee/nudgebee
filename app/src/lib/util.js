import MarkDowns from '@components1/common/MarkDowns';
import { Typography } from '@mui/material';
import { isMarkdown } from 'src/utils/common';

export const getTableDataFromArrayOfObject = (t) => {
  const dataArray = Array.isArray(t) ? t : [t];
  if (dataArray.length === 0) {
    return { headers: [], tableData: [] };
  }
  const headers = Object.keys(dataArray[0]).map((val) => ({ name: val, width: '120px' }));
  const tableData = dataArray.map((item) =>
    Object.entries(item).map(([_key, value]) => ({
      component: (
        <>
          {typeof value === 'string' && isMarkdown(value) ? (
            <MarkDowns
              sx={{
                width: 'fit-content',
              }}
              data={value}
            />
          ) : Array.isArray(value) && value.every((v) => typeof v === 'object') ? (
            value.map((v, index) => <div key={index}>{JSON.stringify(v)}</div>)
          ) : Array.isArray(value) ? (
            value.join(', ')
          ) : typeof value === 'object' ? (
            JSON.stringify(value)
          ) : (
            <Typography>{value}</Typography>
          )}
        </>
      ),
    }))
  );
  return {
    headers,
    tableData,
  };
};

export const calculatePercentage = (recommendedReq, allocatedReq) => {
  const epsilon = 1e-10;
  if (!isNaN(recommendedReq) && !isNaN(allocatedReq) && allocatedReq > epsilon) {
    return Math.abs(((allocatedReq - recommendedReq) / allocatedReq) * 100).toFixed() + '%';
  }
  return '-';
};

export function flattenObject(obj, prefix = '', res = {}) {
  for (const [key, value] of Object.entries(obj || {})) {
    const newKey = prefix ? `${prefix}.${key}` : key;

    if (typeof value === 'object' && value !== null && !Array.isArray(value)) {
      flattenObject(value, newKey, res);
    } else {
      res[newKey] = value;
    }
  }
  return res;
}
