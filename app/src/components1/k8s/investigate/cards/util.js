import { Text } from '@components1/common';
import Datetime from '@components1/common/format/Datetime';
import { Typography } from '@mui/material';
import { formatDurationInTrace, getMsInTimestamp, redK8sErrorCodes as redBanner, snakeToTitleCase } from 'src/utils/common';
import zlib from 'zlib';

export const getTableData = (t) => {
  const headers = t.data.headers || t.data.rows?.[0]?.map((_, i) => `Column ${i + 1}`);
  let apiPath = '';
  let tableInsight = [];
  let convertedJson = (t.data.rows || []).map((row) => {
    const rowData = {};
    headers.forEach((header, index) => {
      rowData[header] = row[index];
    });
    return rowData;
  });

  convertedJson = convertedJson.map((item) => {
    if (Object.hasOwn(item, 'path')) {
      apiPath = item.path;
    }
    if (Object.hasOwn(item, 'payload')) {
      const decodedValue = atob(item.payload);
      return { ...item, payload: decodedValue };
    } else if (Object.hasOwn(item, 'time') && typeof item.time === 'number') {
      return { ...item, time: getMsInTimestamp(item.time) };
    }
    return item;
  });
  const convertedJson2 = convertedJson.map((item) => {
    const isRowRed = Object.values(item).some((value) => typeof value === 'string' && redBanner.includes(value.toLowerCase()));
    const components = Object.entries(item).map(([_key, value]) => ({
      component: <Text value={value} sx={{ color: isRowRed && '#EF4444' }} />,
    }));
    return components;
  });
  if (t.data.table_name.includes('Alert labels')) {
    if (convertedJson.some((row) => row.label === 'severity' && row.value === 'critical')) {
      const message =
        (convertedJson.find((row) => row.label === 'method')?.value || '') +
        ' ' +
        (convertedJson.find((row) => row.label === 'sample' || row.label === 'status')?.value || '') +
        ' ' +
        (convertedJson.find((row) => row.label === 'path')?.value || '');
      if (message?.trim() || '') {
        tableInsight.push({ message: message, severity: 'Critical' });
      }
    }
  }
  if (t.data.table_name.includes('Pod events') || t.data.table_name.includes('Related Events')) {
    const filterData = convertedJson.filter((row) => row.type === 'Warning' || row.type === 'Failed');
    if (filterData && filterData.length > 0) {
      filterData.forEach((e) => {
        if (e.message) {
          tableInsight.push({ message: e.message, severity: 'Critical' });
        }
      });
    }
  }
  return {
    headers: headers,
    convertedJson2: convertedJson2,
    tableInsight: tableInsight,
    httpApiPath: apiPath,
  };
};

export const getTableData1 = (t) => {
  const headers = t.data.headers;
  let convertedJson = t.data.rows.map((row) => {
    const rowData = {};
    headers.forEach((header) => {
      rowData[header] = row[header];
    });
    return rowData;
  });

  const convertedJson2 = convertedJson.map((item) => {
    const components = Object.entries(item).map(([_key, value]) => ({
      component: <Typography>{value}</Typography>,
    }));
    return components;
  });
  return {
    headers: headers,
    convertedJson2: convertedJson2,
  };
};

const getTextRenderer = (k, v) => {
  if (k == 'duration_ms') {
    return formatDurationInTrace(v);
  } else if (k == 'http_response') {
    return atob(v);
  }
  return typeof v === 'object' ? JSON.stringify(v) : v;
};

export const getTableData2 = (t, breakAll = false) => {
  if (!Array.isArray(t) || t.length === 0) {
    return <>No Data</>;
  }
  const headers = Object.keys(t[0]);
  const convertedJson2 = t.map((item) => {
    const isError = item['StatusCode'] == 'STATUS_CODE_ERROR';
    const components = Object.entries(item).map(([key, value]) => ({
      component:
        key === 'Timestamp' || key == 'timestamp' ? (
          <Datetime value={value} />
        ) : (
          <Typography sx={{ color: isError ? 'red' : '#374151', wordBreak: breakAll ? 'break-all' : 'normal', fontSize: '13px' }}>
            {getTextRenderer(key, value)}
          </Typography>
        ),
    }));
    return components;
  });
  return {
    headers: headers,
    convertedJson2: convertedJson2,
  };
};

export const getTableData3 = (data) => {
  if (!data || typeof data !== 'object') {
    return { headers: [], convertedJson2: [] };
  }
  const headers = ['key', 'value'];
  const convertedJson2 = Object.entries(data).map(([key, value]) => [
    {
      component: (
        <Typography
          sx={{
            color: '#374151',
            wordBreak: 'normal',
            fontSize: '13px',
          }}
        >
          {snakeToTitleCase(key)}
        </Typography>
      ),
    },
    {
      component: (
        <Typography
          sx={{
            color: '#374151',
            wordBreak: 'break-all',
            fontSize: '13px',
          }}
        >
          {value}
        </Typography>
      ),
    },
  ]);

  return { headers, convertedJson2 };
};

export const getTableData4 = (t) => {
  const headers = ['key', 'value'];
  const convertedJson2 = [];

  t.forEach((item) => {
    const isError = item['StatusCode'] == 'STATUS_CODE_ERROR';

    Object.entries(item).forEach(([key, value]) => {
      const components = [
        {
          component: (
            <Typography sx={{ color: isError ? 'red' : '#374151', wordBreak: 'normal', fontSize: '13px' }}>{snakeToTitleCase(key)}</Typography>
          ),
        },
        {
          component:
            key === 'Timestamp' || key === 'timestamp' || key === 'time' || key === 'created_at' ? (
              <Datetime value={value} />
            ) : (
              <Typography sx={{ color: isError ? 'red' : '#374151', wordBreak: 'break-all', overflowWrap: 'anywhere', fontSize: '13px' }}>
                {getTextRenderer(key, value)}
              </Typography>
            ),
        },
      ];
      convertedJson2.push(components);
    });
  });

  return {
    headers: headers,
    convertedJson2: convertedJson2,
  };
};

export function unzipData(gzData) {
  return new Promise((resolve, reject) => {
    zlib.gunzip(gzData, (err, unzippedBuffer) => {
      if (err) {
        console.error('Error unzipping the file:', err);
        reject(err);
      } else {
        const unzippedData = unzippedBuffer.toString('utf8');
        resolve(unzippedData);
      }
    });
  });
}

export const base64Converter = (data) => {
  if (typeof data !== 'string') {
    return null;
  }
  const cleanedData = data.replace(/^b'|^b"|'"$/g, '');

  try {
    const bufferData = Buffer.from(cleanedData, 'base64');
    return bufferData;
  } catch {
    console.error('Invalid base64 input');
    return null;
  }
};
