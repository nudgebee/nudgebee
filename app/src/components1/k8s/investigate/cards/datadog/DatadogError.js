import ListTextIcon from '@assets/kubernetes/list-text-icon.svg';
import { Box, Chip, Typography } from '@mui/material';
import MarkDowns from '@components1/common/MarkDowns';
import React from 'react';

class DatadogError {
  constructor() {
    this.id = 'DatadogErrorCard';
    this.icon = ListTextIcon;
    this.text = 'Datadog Error';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.event = {};
    this.properties = [];
    this.stacktrace = '';
    this.title = '';
  }

  /**
   * Determines if this description can render the given event.
   * @param {Array} evidenceData The list of evidences.
   * @param {object} event The event payload.
   * @returns {boolean} True if the event can be rendered, false otherwise.
   */
  canRenderContent = async (evidenceData) => {
    let event = null;
    for (const evidence of evidenceData) {
      if (evidence?.additional_info?.action_name === 'datadog_error_tracking_issue') {
        event = evidence;
        break;
      }
    }
    if (!event || !event.data || !event.data.data || !event.data.data.attributes) {
      return false;
    }
    this.event = event;
    this.title = this._getTitle();
    this.text = this.title;
    this.properties = this._getProperties();
    this.stacktrace = this._getStacktrace();
    this.insightData = event?.insight || [];

    this.renderContent = true;
    return true;
  };

  _getTitle = () => {
    const attributes = this.event?.data?.data?.attributes;
    const errorType = attributes?.error?.type;
    const service = attributes?.service;

    if (errorType && service) {
      return `Error: ${errorType} in ${service}`;
    }

    return this.event?.additional_info?.action_title || 'Datadog Error';
  };

  _getProperties = () => {
    const attributes = this.event?.data?.data?.attributes;
    if (!attributes) {
      return [];
    }

    const props = [];
    const addProp = (key, value, isCode = false) => {
      if (value) {
        props.push({ key, value, isCode });
      }
    };

    addProp('Error Message', attributes.custom?.error?.message);
    addProp('Resource', attributes.resource_name, true);
    addProp('Service', attributes.service, true);
    addProp('Operation', attributes.operation_name, true);
    addProp('Environment', attributes.env, true);
    addProp('Host', attributes.host, true);
    addProp('URL', attributes.custom?.http?.url, true);
    addProp('Language', attributes.custom?.language, true);
    addProp('Version', attributes.custom?.version, true);
    addProp('Error File', attributes.custom?.error?.file, true);
    addProp('Issue ID', attributes.custom?.issue?.id, true);
    addProp('Trace ID', attributes.trace_id, true);
    addProp('Span ID', attributes.span_id, true);
    addProp('Start Time', attributes.start_timestamp ? new Date(attributes.start_timestamp).toUTCString() : null);
    addProp('First Seen', attributes.custom?.issue?.first_seen ? new Date(attributes.custom.issue.first_seen).toUTCString() : null);

    if (attributes.tags && attributes.tags.length > 0) {
      props.push({ key: 'Tags', value: attributes.tags, isTags: true });
    }

    return props;
  };

  _getStacktrace = () => {
    return this.event?.data?.data?.attributes?.custom?.error?.stack;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    const components = [() => this.renderProperties()];
    if (this.stacktrace) {
      components.push(() => this.renderStacktrace());
    }
    return components;
  };

  renderProperties = () => {
    return (
      <Box sx={{ p: 2 }}>
        {this.properties.map((prop, index) => {
          if (prop.isTags) {
            return (
              <Box key={index} sx={{ mb: 1 }}>
                <Typography variant='body2' sx={{ fontWeight: 'bold', mb: 0.5 }}>
                  {prop.key}:
                </Typography>
                <Box sx={{ display: 'flex', flexWrap: 'wrap', gap: 0.5 }}>
                  {prop.value.map((tag, tagIndex) => (
                    <Chip key={tagIndex} label={tag} size='small' sx={{ backgroundColor: '#e0e0e0' }} />
                  ))}
                </Box>
              </Box>
            );
          }
          return (
            <Box key={index} sx={{ display: 'flex', mb: 1, alignItems: 'flex-start' }}>
              <Typography variant='body2' sx={{ fontWeight: 'bold', minWidth: '120px', flexShrink: 0 }}>
                {prop.key}:
              </Typography>
              {prop.isCode ? (
                <Typography
                  variant='body2'
                  component='code'
                  sx={{ fontFamily: 'monospace', backgroundColor: '#f5f5f5', p: '2px 4px', borderRadius: '4px', wordBreak: 'break-all' }}
                >
                  {prop.value}
                </Typography>
              ) : (
                <Typography variant='body2' sx={{ wordBreak: 'break-word' }}>
                  {prop.value}
                </Typography>
              )}
            </Box>
          );
        })}
      </Box>
    );
  };

  renderStacktrace = () => {
    return (
      <Box sx={{ p: 2 }}>
        <Typography variant='h6' sx={{ mb: 1 }}>
          Stack Trace
        </Typography>
        <MarkDowns data={`\`\`\`\n${this.stacktrace}\n\`\`\``} />
      </Box>
    );
  };
}

export default DatadogError;
