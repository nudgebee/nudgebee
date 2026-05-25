import { AllEventsIcon } from '@assets';
import React from 'react';
import { Grid, Typography } from '@mui/material';

class CustomAction {
  constructor() {
    this.id = 'CustomAction';
    this.icon = AllEventsIcon;
    this.text = 'Custom Action';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.customActionData = {};
    this.tableData = {};
    this.event = {};
    this.isFetching = false;
  }

  canRenderContent = async (evidenceData, event) => {
    this.renderContent = false;
    this.event = event;
    const jsonData = evidenceData?.find((item) => item.type === 'json') || {};
    if (jsonData?.data) {
      try {
        const parsedData = JSON.parse(jsonData.data);
        if (parsedData.type == 'pod_script_run_enricher') {
          this.customActionData = parsedData;
          this.renderContent = true;
        }
      } catch {
        this.renderContent = false;
      }
    }
    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderCustomAction(this.customActionData)];
  };

  renderCustomAction = (ca) => {
    return (
      <>
        {ca.image && <Typography>Image: {ca.image}</Typography>}
        {ca.command && <Typography>Command: {ca.command}</Typography>}
        {ca.secret && <Typography>Secret: {ca.secret}</Typography>}
        {ca.response ? (
          <>
            <Typography>Response:</Typography>
            <Grid
              container
              sx={{
                marginBottom: '8px',
                fontSize: '14px',
                color: '#737373',
                wordBreak: 'break-word',
                pre: {
                  textWrap: 'inherit',
                },
              }}
            >
              <pre>{ca.response.replace(/\\n/g, '\n')}</pre>
            </Grid>
          </>
        ) : null}
      </>
    );
  };
}

export default CustomAction;
