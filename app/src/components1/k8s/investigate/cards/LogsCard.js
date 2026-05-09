import React, { useState } from 'react';
import ConsoleLogOutput from '@components1/common/ConsoleLogOutput';
import { base64Converter, unzipData } from './util';
import LogsIcon from '@assets/investigation/logs-blue.svg';
import { redLogsErrorCodes as keywords, libraryErrors } from 'src/utils/common';

class LogsCard {
  constructor() {
    this.id = 'LogsCard';
    this.icon = LogsIcon;
    this.text = 'Review Log Files';
    this.resolveButton = false;
    this.logs = [
      {
        fileName: '',
        logs: '',
        additionalInfo: {},
      },
    ];
    this.insightData = [];
    this.renderContent = false;
  }

  canRenderContent = async (evidenceData, _event) => {
    const isValidLogEvidence = evidenceData.some((item) => {
      const isGz = (item.type === 'gz' && item.filename?.endsWith('log.gz')) || item.filename?.endsWith('.gz');

      const isExcluded = item.filename?.endsWith('pprof.gz') && item.additional_info?.actual_action_name === 'pod_profiler';

      return isGz && !isExcluded && item?.additional_info?.actual_action_name != 'datadog_logs';
    });
    if (isValidLogEvidence) {
      this.renderContent = true;
      let logObject = evidenceData.filter((item) => item.type === 'gz' || item.filename?.endsWith('.gz'));
      const getInsights = logObject
        .filter((p) => p && p.insight && p.insight.length > 0)
        .map((g) => g.insight)
        .flat();
      if (getInsights.length > 0) {
        this.insightData = this.insightData.concat(getInsights);
      }

      if (logObject?.length > 0) {
        const logData = [];
        for (const d of logObject) {
          let keywordCount = 0;
          if (logData.some((log) => log.fileName === d.filename)) {
            continue;
          }

          let gzObject = d.data;
          const gzData = base64Converter(gzObject);

          try {
            let unzippedData = await unzipData(gzData);
            const combinedData = unzippedData.replace(/\n$/, '');
            const lines = combinedData.split('\n');
            let line = '';
            let firstErrorMessageFromLast = '';
            for (let i = lines.length - 1; i >= 0; i--) {
              line = lines[i];
              const containsKeyword = keywords.some((keyword) => {
                const regex = new RegExp(`\\b(?:${keyword})\\b(?![^"]*INFO(?:[^"]*"(?:(?!INFO).)*?")*[^"]*$)`);
                return regex.test(line.toLowerCase());
              });
              const containsErrorLibrary = libraryErrors.some((keyword) => {
                const regex = new RegExp(`(?<!w)${keyword.toLowerCase()}(?!w)`);
                return regex.test(line.toLowerCase());
              });

              if (containsKeyword || containsErrorLibrary) {
                keywordCount = keywordCount + 1;
                if (!firstErrorMessageFromLast) {
                  firstErrorMessageFromLast = line.trim();
                }
              }
            }
            if (keywordCount > 0) {
              const podName = d.additional_info?.pod_name ? ` in pod ${d.additional_info.pod_name}` : '';
              const message = `${keywordCount} Failure Message${keywordCount > 1 ? 's' : ''}${podName}`;
              this.insightData.push({ message, severity: 'High' });
            }

            logData.push({
              fileName: d.filename,
              logs: combinedData,
              additionalInfo: d.additional_info || {},
            });
          } catch {
            console.error('Error processing log data');
          }
        }

        this.logs = logData;
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderLogs(this.logs)];
  };

  // Helper function to create tab label from additionalInfo
  getTabLabel = (additionalInfo, fileName) => {
    const parts = [];
    if (additionalInfo?.namespace) {
      parts.push(`Namespace: ${additionalInfo.namespace}`);
    }
    if (additionalInfo?.pod_name) {
      parts.push(`Pod: ${additionalInfo.pod_name}`);
    }
    if (additionalInfo?.container_name) {
      parts.push(`Container: ${additionalInfo.container_name}`);
    }

    return parts.length > 0 ? parts.join(' | ') : fileName;
  };

  // Helper function to check if log has meaningful additionalInfo
  hasAdditionalInfo = (additionalInfo) => {
    return additionalInfo?.namespace || additionalInfo?.pod_name || additionalInfo?.container_name;
  };

  renderLogs = (logs) => {
    // Filter logs that have additionalInfo for tabs
    const logsWithTabs = logs.filter((log) => this.hasAdditionalInfo(log.additionalInfo));
    const logsWithoutTabs = logs.filter((log) => !this.hasAdditionalInfo(log.additionalInfo));

    return (
      <div>
        {/* Render tabs for logs with additionalInfo */}
        {logsWithTabs.length > 0 && <LogsTabs logs={logsWithTabs} getTabLabel={this.getTabLabel} />}

        {/* Render non-tabbed logs */}
        {logsWithoutTabs.map((log, index) => (
          <div key={log.fileName}>
            <ConsoleLogOutput data={log.logs} />
            {index < logsWithoutTabs.length - 1 && <br />}
          </div>
        ))}
      </div>
    );
  };
}

// Tabs component for logs with additionalInfo
const LogsTabs = ({ logs, getTabLabel }) => {
  const [activeTab, setActiveTab] = useState(0);

  const handleTabClick = (index) => {
    setActiveTab(index);
  };

  const handleTabKeyDown = (event, index) => {
    switch (event.key) {
      case 'Enter':
      case ' ':
        event.preventDefault();
        setActiveTab(index);
        break;
      case 'ArrowLeft':
        event.preventDefault();
        setActiveTab(index > 0 ? index - 1 : logs.length - 1);
        break;
      case 'ArrowRight':
        event.preventDefault();
        setActiveTab(index < logs.length - 1 ? index + 1 : 0);
        break;
      default:
        break;
    }
  };

  const tabStyle = {
    display: 'inline-block',
    padding: '8px 8px',
    margin: '4px 2px',
    backgroundColor: '#f0f0f0',
    cursor: 'pointer',
    borderRadius: '4px',
  };

  const activeTabStyle = {
    ...tabStyle,
    backgroundColor: '#fff',
  };

  const focusStyle = {
    boxShadow: '0 0 0 2px #007acc',
  };

  const tabContentStyle = {
    border: '1px solid #ccc',
    padding: '0px 16px 16px 16px',
    backgroundColor: '#fff',
    borderRadius: '8px',
  };

  const _tabHeaderStyle = {
    marginBottom: '16px',
    fontWeight: 'bold',
    fontSize: '14px',
    color: '#555',
  };

  return (
    <div style={{ marginBottom: '20px' }}>
      {/* Tab Headers */}
      <div role='tablist' style={{ color: '#374151', fontSize: '14px' }}>
        {logs.map((log, index) => (
          <div
            key={`tab-${log.fileName}`}
            role='tab'
            tabIndex={activeTab === index ? 0 : -1}
            aria-selected={activeTab === index}
            aria-controls={`tabpanel-${index}`}
            style={activeTab === index ? activeTabStyle : tabStyle}
            onClick={() => handleTabClick(index)}
            onKeyDown={(e) => handleTabKeyDown(e, index)}
            onFocus={(e) => {
              e.target.style.boxShadow = focusStyle.boxShadow;
            }}
            onBlur={(e) => {
              e.target.style.boxShadow = 'none';
            }}
          >
            {getTabLabel(log.additionalInfo, log.fileName)}
          </div>
        ))}
      </div>

      {/* Tab Content */}
      <div role='tabpanel' id={`tabpanel-${activeTab}`} aria-labelledby={`tab-${activeTab}`} style={tabContentStyle}>
        {logs[activeTab] && <ConsoleLogOutput data={logs[activeTab].logs} />}
      </div>
    </div>
  );
};

export default LogsCard;
