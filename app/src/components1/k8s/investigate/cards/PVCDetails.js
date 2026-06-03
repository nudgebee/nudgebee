import Datetime from '@components1/common/format/Datetime';
import { Box, Typography } from '@mui/material';
import DescriptionIcon from '@assets/investigation/description-icon.svg';

class PVCDetails {
  constructor() {
    this.id = 'PVCDetails';
    this.icon = DescriptionIcon;
    this.text = 'PVC Details';
    this.resolveButton = false;
    this.insightData = [];
    this.renderContent = false;
    this.pvcDetails = {};
  }

  canRenderContent = async (evidenceData, _event) => {
    const filterJSONTypes = evidenceData.filter((item) => item.type === 'json');
    let filteredPVCObject = {};

    for (const item of filterJSONTypes) {
      if (!item?.data || typeof item.data !== 'string') {
        continue;
      }
      try {
        const parsedData = JSON.parse(item.data);
        if (parsedData.type === 'pvc') {
          filteredPVCObject = parsedData;
          break;
        }
      } catch {
        // do nothing, continue to next item
      }
    }

    if (filteredPVCObject && Object.keys(filteredPVCObject).length > 0) {
      const originalAnnotations = filteredPVCObject.metadata.annotations || {};
      const filteredAnnotations = Object.keys(originalAnnotations)
        .filter((key) => !key.includes('last-applied-configuration'))
        .reduce((acc, key) => {
          acc[key] = originalAnnotations[key];
          return acc;
        }, {});

      const accessModes = Array.isArray(filteredPVCObject.spec.accessModes)
        ? filteredPVCObject.spec.accessModes.join(', ')
        : filteredPVCObject.spec.accessModes || '';

      this.renderContent = true;
      this.pvcDetails = {
        volumeName: filteredPVCObject.spec.volumeName,
        storageClassName: filteredPVCObject.spec.storageClassName,
        requestStorage: filteredPVCObject.spec.resources.requests.storage,
        accessModes: accessModes,
        creationTimestamp: filteredPVCObject.metadata.creationTimestamp,
        finalizers: filteredPVCObject.metadata.finalizers,
        annotations: filteredAnnotations,
        capacity: filteredPVCObject.status.capacity.storage,
      };
    }

    return this.renderContent;
  };

  getHighLightsData = () => {
    return this.insightData;
  };

  getContentComponents = () => {
    return [() => this.renderPVCDetails(this.pvcDetails)];
  };

  renderPVCDetails = (details) => {
    return (
      <Box display='flex' flexDirection='column' gap={1}>
        <Typography>
          <strong>Volume Name:</strong> {details.volumeName}
        </Typography>
        <Typography>
          <strong>Storage Class Name:</strong> {details.storageClassName}
        </Typography>
        <Typography>
          <strong>Request Storage:</strong> {details.requestStorage}
        </Typography>
        <Typography>
          <strong>Capacity:</strong> {details.capacity}
        </Typography>
        <Typography>
          <strong>Access Modes:</strong> {details.accessModes}
        </Typography>
        <Box display='flex' alignItems='center'>
          <Typography component='span' fontWeight='bold'>
            Creation Timestamp:
          </Typography>
          &nbsp;
          <Datetime value={details.creationTimestamp} />
        </Box>

        <Typography>
          <strong>Finalizers:</strong> {details.finalizers?.join(', ')}
        </Typography>
        <Typography>
          <strong>Annotations:</strong> {JSON.stringify(details.annotations, null, 2)}
        </Typography>
      </Box>
    );
  };
}

export default PVCDetails;
