import { Box, Typography } from '@mui/material';
import CopyableText from '@components1/common/CopyableText';
import { plusIcon } from '@assets';
import BarChart from '@components1/common/charts/BarChart';
import { convertStringCase } from 'src/utils/common';
import apiRecommendations from '@api1/recommendation';
import React from 'react';

export const MENU_ITEMS = [
  {
    disabled: true,
    label: 'Start Instance',
    id: 0,
  },
  {
    disabled: true,
    label: 'Stop Instance',
    id: 1,
  },
  {
    disabled: true,
    label: 'Terminate Instance',
    id: 2,
  },
  {
    disabled: true,
    icon: plusIcon,
    label: 'Add Alarms',
    id: 3,
  },
];

export const parseJsonToKeyValue = (objData: any) => {
  const keys = Object.keys(objData);
  const keyset: any = [];
  keys.forEach((e) => {
    const upperCaseWords = e.match(/([a-z]+|[A-Z]['a-z]+)/g)?.map((text) => text.charAt(0).toUpperCase() + text.slice(1, text.length));
    let value = objData[e];
    if (typeof value === 'string' && /^\d+\.\d{3,}$/.test(value)) {
      value = parseFloat(value).toFixed(2);
    }
    keyset.push({ key: e, label: upperCaseWords?.join(' '), value, type: typeof objData[e] });
  });
  return keyset;
};

export const DataBlock = ({ title, data, children, isCopyable = true, showCopyIconOnHover = false }: any) => {
  const isList = Array.isArray(data);
  return (
    <Box>
      {title && (
        <Typography color='#737373' fontSize={'12px'} fontWeight={600} mb={'1px'}>
          {title}
        </Typography>
      )}
      {isList ? (
        <Box component='ul' sx={{ pl: 2, m: 0 }}>
          {data.map((item, index) => {
            let renderItem = item;
            if (typeof item === 'object' && item !== null) {
              if (item.price !== undefined && item.instanceType !== undefined) {
                renderItem = `${item.instanceType} ($${item.price})`;
              } else {
                renderItem = JSON.stringify(item);
              }
            }
            return (
              <Typography component='li' key={`${String(item)}-${index}`} fontSize='13px' sx={{ listStyleType: 'disc' }}>
                {isCopyable ? (
                  <CopyableText copyableText={String(renderItem)} showCopyIconOnHover={showCopyIconOnHover}>
                    {renderItem}
                  </CopyableText>
                ) : (
                  renderItem
                )}
              </Typography>
            );
          })}
        </Box>
      ) : (
        <React.Fragment>
          {data && isCopyable ? (
            <Typography fontSize={'13px'}>
              <CopyableText copyableText={data} iconColor={undefined} onCopy={undefined} showCopyIconOnHover={showCopyIconOnHover}>
                {data}
              </CopyableText>
            </Typography>
          ) : (
            <Typography fontSize={'13px'}>{data}</Typography>
          )}
        </React.Fragment>
      )}
      {children}
    </Box>
  );
};

function checkValuesAndTimestampsExistsInObject(jsonObject: any) {
  function hasValuesAndTimestamps(obj: any) {
    if (
      Object.prototype.hasOwnProperty.call(obj, 'values') &&
      Array.isArray(obj.values) &&
      Object.prototype.hasOwnProperty.call(obj, 'timestamps') &&
      Array.isArray(obj.timestamps)
    ) {
      return true;
    }
    for (const key in obj) {
      if (Object.prototype.hasOwnProperty.call(obj, key) && typeof obj[key] === 'object' && obj[key] !== null) {
        if (hasValuesAndTimestamps(obj[key])) {
          return true;
        }
      }
    }
    return false;
  }

  return hasValuesAndTimestamps(jsonObject);
}

function findPaths(obj: any, key: any, path = '') {
  let paths: any[] = [];
  for (const k in obj) {
    if (Object.prototype.hasOwnProperty.call(obj, k)) {
      if (k === key && Array.isArray(obj[k])) {
        paths.push(`${path}.${k}`.replace(/^\./, ''));
      } else if (k === key && typeof obj[k] === 'string') {
        paths.push(`${path}.${k}`.replace(/^\./, ''));
      } else if (typeof obj[k] === 'object' && obj[k] !== null) {
        paths = paths.concat(findPaths(obj[k], key, `${path}.${k}`));
      }
    }
  }
  return paths;
}

function getDataByPath(obj: any, path: any) {
  if (!path || typeof path !== 'string') {
    return undefined;
  }
  const keys = path.split('.');
  let _result = obj;
  for (const key of keys) {
    if (Object.prototype.hasOwnProperty.call(_result, key)) {
      _result = _result[key];
    } else {
      return undefined;
    }
  }
  return _result;
}

export const DrilldownDetails = ({ data, showCopyIconOnHover = false }: any) => {
  const keyset: any = parseJsonToKeyValue(data);
  const graphData = checkValuesAndTimestampsExistsInObject(data);
  const allGraphData: any = {};
  let values = [];
  let labels = [];
  if (graphData) {
    const nameKeys = findPaths(data, 'name');
    const statisticsKeys = findPaths(data, 'statistics');
    const valueKeys = findPaths(data, 'values');
    const timestampKeys = findPaths(data, 'timestamps');
    if (nameKeys && nameKeys.length > 0) {
      for (const [index, nk] of nameKeys.entries()) {
        values = getDataByPath(data, valueKeys[index]) ?? [];
        labels = getDataByPath(data, timestampKeys[index])?.map((f: any) => new Date(f).toLocaleString()) ?? [];
        let nameKeyData = getDataByPath(data, nk) ?? '';
        nameKeyData = nameKeyData != 'CPUUtilization' ? `${convertStringCase(nameKeyData)}` : `CPU Utilization`;
        const statisticsKeyData = getDataByPath(data, statisticsKeys[index]) ?? '';
        const key = `${nameKeyData} | ${statisticsKeyData}`;
        allGraphData[key] = {
          values: values,
          labels: labels,
        };
      }
    }
  }
  return (
    <div>
      <Box
        sx={{
          display: 'grid',
          gridTemplateColumns: 'repeat(3, 1fr)',
          columnGap: '48px',
          rowGap: '32px',
          mb: '25px',
          position: 'relative',
          '& > *': {
            overflow: 'hidden',
            wordBreak: 'break-word',
          },
          background: `
        linear-gradient(to right,
        transparent calc(33.33% - 9px),
        #e5e7eb calc(33.33% - 9px),
        #e5e7eb calc(33.33% - 8px),
        transparent calc(33.33% - 8px),
        transparent calc(66.66% + 8px),
        #e5e7eb calc(66.66% + 8px),
        #e5e7eb calc(66.66% + 9px),
        transparent calc(66.66% + 9px)
      )
    `,
        }}
      >
        {keyset?.map((item: any) => {
          return (
            (item.type === 'string' || Array.isArray(item.value)) &&
            item.value !== '' && <DataBlock key={item.key} title={item.label} data={item.value} showCopyIconOnHover={showCopyIconOnHover} />
          );
        })}
      </Box>
      {allGraphData && Object.keys(allGraphData).length > 0 && (
        <>
          <Typography color='#737373' fontSize={'12px'} fontWeight={400} mb={'1px'}>
            More Details
          </Typography>
          {Object.keys(allGraphData).map((r) => (
            <BarChart key={r} chartTitle={r} data={allGraphData[r].values} labels={allGraphData[r].labels} />
          ))}
        </>
      )}
    </div>
  );
};

export const getTicketDescription = (ticketData: any) => {
  if (!ticketData) {
    return '';
  }

  const recommendationDetails = apiRecommendations.getRecommendationDetails(ticketData.category, ticketData.rule_name);

  let serviceName = 'N/A';
  let objectName = 'N/A';

  if (recommendationDetails?.serviceName) {
    serviceName = recommendationDetails.serviceName;
  } else if (ticketData.recommendation?.service_name) {
    serviceName = ticketData.recommendation.service_name;
  } else if (ticketData.account_object_id) {
    const objectParts = ticketData.account_object_id.split(':');
    if (objectParts.length === 7) {
      serviceName = objectParts[2];
      objectName = objectParts[6];
    }
  }

  // Try to get objectName from multiple sources
  if (objectName === 'N/A') {
    if (ticketData.objectName) {
      objectName = ticketData.objectName;
    } else if (ticketData.resource_name) {
      objectName = ticketData.resource_name;
    } else if (ticketData.account_object_id) {
      const objectParts = ticketData.account_object_id.split(':');
      if (objectParts.length === 7) {
        objectName = objectParts[6];
      }
    }
  }

  // Handle security hub specific logic
  if (serviceName === 'securityhub' && ticketData.recommendation) {
    const securityRecommendationDetails = {
      title: ticketData.recommendation?.Title,
      description: ticketData.recommendation?.Description,
      serviceName: ticketData.recommendation?.ServiceName,
      recommendations: [],
      mitigations: [
        `${ticketData.recommendation?.Remediation?.Recommendation?.Text} - ${ticketData.recommendation?.Remediation?.Recommendation?.Url}`,
      ],
    };

    if (securityRecommendationDetails?.serviceName) {
      serviceName = securityRecommendationDetails.serviceName;
    }
  }

  let description = `Recommendation: ${ticketData.rule_name || 'N/A'}
Service: ${serviceName}
Instance: ${objectName}
Severity: ${ticketData.severity || 'N/A'}`;

  // Add estimated savings if available
  if (ticketData.estimated_savings) {
    description += `
Estimated Savings: $${ticketData.estimated_savings.toFixed(2)}`;
  }

  // Add details based on available data
  const details = recommendationDetails?.recommendations?.[0] || recommendationDetails?.description || ticketData.recommendation?.reason || 'N/A';

  description += `
Details: ${details}`;

  return description;
};
