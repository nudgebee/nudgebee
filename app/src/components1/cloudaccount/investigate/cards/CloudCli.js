import { titleCase } from '@lib/formatter';
import { safeJSONParse } from 'src/utils/common';
import { Typography } from '@mui/material';
import MarkDowns from '@components1/common/MarkDowns';
import { TerminalIcon } from '@assets';

class CloudCli {
  constructor(data, _event) {
    this.id = `CloudCli`;
    this.text = titleCase(data?.additional_info?.title || 'Server Process Details') || 'Server Process Details';
    this.icon = TerminalIcon;
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.enricherData = data;
    this.markdown = {
      command: '',
      markdown: '',
    };
    this.disabled = data?.additional_info?.status == 'skipped';
  }

  canRenderContent = async () => {
    this.renderContent = false;
    const isCloudSsh = this.enricherData?.additional_info?.action_name === 'cloud_cli';
    if (isCloudSsh) {
      const cloudSshParsedData = safeJSONParse(this.enricherData.data);
      if (cloudSshParsedData) {
        const command = cloudSshParsedData.command;
        let markdown = cloudSshParsedData.stdout;
        const stdOut = safeJSONParse(cloudSshParsedData.stdout);
        if (stdOut) {
          const value = stdOut.value;
          if (value?.length) {
            const message = value?.[0]?.message || '';
            const stdoutMatch = message.match(/\[stdout\]\n([\s\S]*?)(?=\n\[stderr\]|$)/);
            const stdoutRaw = stdoutMatch ? stdoutMatch[1].trim() : message.trim();

            // Add line numbers for better readability
            const stdoutLines = stdoutRaw
              .split('\n')
              .map((line, index) => `${String(index + 1).padStart(3, '0')} | ${line}`)
              .join('\n');
            markdown = `### 🟢 STDOUT\n\n\`\`\`bash\n${stdoutLines}\n\`\`\``;
          }
        }
        this.markdown = {
          command,
          markdown,
        };
        this.renderContent = true;
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderTableData()];
  };

  renderTableData = () => {
    return (
      <>
        {this.markdown.command && <Typography>{this.markdown.command}</Typography>}
        {this.markdown.markdown ? <MarkDowns data={this.markdown.markdown} /> : <Typography>No Data Available</Typography>}
      </>
    );
  };
}

export default CloudCli;
