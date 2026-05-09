import { titleCase } from '@lib/formatter';
import { safeJSONParse } from 'src/utils/common';
import { Typography } from '@mui/material';
import MarkDowns from '@components1/common/MarkDowns';
import { TerminalIcon } from '@assets';

class CloudSsh {
  constructor(data, _event) {
    this.id = `CloudSsh`;
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
    const isCloudSsh = this.enricherData?.additional_info?.action_name === 'ssh';
    if (isCloudSsh) {
      const cloudSshParsedData = safeJSONParse(this.enricherData.data);
      if (cloudSshParsedData) {
        const command = cloudSshParsedData.command;
        const markdown = cloudSshParsedData.stdout;
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

export default CloudSsh;
