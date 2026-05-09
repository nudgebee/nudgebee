import { LogsIcon } from '@assets';
import SignozDatadogLogs from '@components1/k8s/details/SignozDatadogLogs';
import { base64Converter, unzipData } from './util';
import { safeJSONParse } from 'src/utils/common';

let logCardIdx = 0;

class SignozDatadogLogCard {
  constructor(data, _event) {
    this.id = `SignozDatadogLogCard_${logCardIdx++}`;
    this.icon = LogsIcon;
    this.text = data?.additional_info?.title || 'Review Log';
    this.resolveButton = false;
    this.logs = data;
    this.insightData = [];
    this.renderContent = false;
    this.logsData = [];
  }

  canRenderContent = async () => {
    this.renderContent = false;
    const isGz = (this.logs?.type === 'gz' && this.logs?.filename?.endsWith('log.gz')) || this.logs?.filename?.endsWith('.gz');
    if (isGz) {
      let gzObject = this.logs.data;
      const gzData = base64Converter(gzObject);
      try {
        let unzippedData = await unzipData(gzData);
        const combinedData = unzippedData.replace(/\n$/, '');
        const parsedLogData = safeJSONParse(combinedData);
        const mappedData = parsedLogData.map((item) => ({
          timestamp: item.attributes.timestamp || '',
          severity: item.attributes.status || '',
          message: item.attributes.message || '',
          labels: { ...item.attributes },
        }));
        this.logsData = mappedData;
        this.renderContent = true;
      } catch {
        console.error('Error processing log data');
      }
    } else {
      const rawData = this.logs?.text || this.logs?.data || '{}';
      const text = rawData;
      try {
        let parsedText = typeof rawData === 'string' ? safeJSONParse(text) : rawData;
        if (parsedText && this.logs.additional_info?.action_name?.includes('logs')) {
          parsedText = parsedText.data;
        }
        const isNewFormat =
          Array.isArray(parsedText) &&
          parsedText.length > 0 &&
          typeof parsedText[0].timestamp === 'string' &&
          typeof parsedText[0].message === 'string';

        if (!isNewFormat) {
          if (Object.keys(parsedText).length > 0) {
            const list = parsedText?.data?.result || [];
            if (list.length > 0) {
              const transformed =
                list?.[0]?.list?.map((item) => {
                  const { timestamp, data } = item;
                  const {
                    body,
                    severity_text,
                    attributes_string = {},
                    attributes_bool = {},
                    attributes_float64 = {},
                    attributes_int64 = {},
                    resources_string = {},
                    ...rest
                  } = data;

                  const labels = {
                    ...attributes_string,
                    ...attributes_bool,
                    ...attributes_float64,
                    ...attributes_int64,
                    ...resources_string,
                    ...rest,
                  };

                  return {
                    timestamp,
                    message: body || '',
                    labels,
                    severity: severity_text || '',
                  };
                }) || [];
              this.logsData = transformed;
            }
          }
        } else {
          this.logsData = parsedText.map((item) => ({
            timestamp: item.timestamp || '',
            message: item.message || '',
            labels: item.labels || {},
            severity: item.severity || '',
          }));
        }
        this.insightData = this.logs?.insight || [];
        this.renderContent = true;
      } catch (e) {
        console.error('Failed to parse log data in SignozDatadogLogCard:', e);
        this.renderContent = false;
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderSignozLogs()];
  };

  renderSignozLogs = () => {
    return <SignozDatadogLogs logData={this.logsData} />;
  };
}

export default SignozDatadogLogCard;
