import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import Text from '@shared/format/Text';
import { LogDate } from '@components/k8s/common/LogDate';

// Extracted from the now-deleted KubernetesLogStash.tsx (that component was
// unreachable — nothing rendered it — but this formatter is still used by
// KubernetesLLMRequestResponseV2.jsx and ToolDetails.jsx to render ES/logstash
// hits as table rows.
export const mapToTableData = (data: any, getMenuItem: any = null, onMenuClick: any = null) => {
  const convertedJson = data.map((row: any) => row._source);
  const convertedJson2 = convertedJson.map((item: any) => {
    let message = item['message'] ?? item['log'] ?? item['msg'];
    if (!message) {
      const msg2 = item['kubernetes'] ?? item['_source'] ?? item;
      if (msg2) {
        message = JSON.stringify(msg2);
      }
    }
    let timestamp = item['@timestamp'];
    if (item['@timestamp']) {
      timestamp = new Date(item['@timestamp']).getTime();
    } else if (!timestamp && (item?.updated_at || item?.time)) {
      timestamp = new Date(item?.updated_at ?? item?.time).getTime();
    }
    const row = [
      {
        component: <LogDate timestamp={timestamp} log={message} />,
        drilldownQuery: item,
      },
      {
        component: <Text showAutoEllipsis value={message} />,
      },
    ];

    if (getMenuItem && onMenuClick) {
      row.push({
        component: <ThreeDotsMenu menuItems={getMenuItem()} data={item['message']} onMenuClick={onMenuClick} />,
      });
    }

    return row;
  });
  return convertedJson2;
};
