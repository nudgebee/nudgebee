/**
 * @deprecated Runbook functionality has been replaced by Workflows.
 * This file is kept for backward compatibility with existing executions.
 * TODO: Remove once workflow migration is complete.
 */
import apiKubernetes from '@api1/kubernetes';
import { Select } from '@components1/ds/Select';
import { Tabs } from '@components1/ds/Tabs';
import { ExpandMore } from '@mui/icons-material';
import { Accordion, AccordionDetails, AccordionSummary, Box, Typography, CircularProgress } from '@mui/material';
import { Checkbox } from '@components1/ds/Checkbox';
import React, { useEffect, useState } from 'react';
import type { WorkloadObject } from 'src/utils/common';
import { ds } from 'src/utils/colors';

interface RunbookTargetResourceProps {
  handleChildComponentChange: (value: any, type: string) => void;
  selectedApplications: WorkloadObject[];
  selectedCluster: any;
  selectedNamespace: string | string[];
  reviewRunbook: boolean;
  multipleNamespace?: boolean;
  viewOnlyMode?: boolean;
  hideTabs?: boolean;
}

interface CheckedItems {
  [key: string]: boolean;
}

const RunbookTargetResource: React.FC<RunbookTargetResourceProps> = ({
  handleChildComponentChange,
  selectedApplications,
  selectedCluster,
  selectedNamespace,
  reviewRunbook = false,
  multipleNamespace = false,
  viewOnlyMode = false,
  hideTabs = false,
}) => {
  const targetResourceTypes = [{ id: 'applications', label: 'Applications' }];
  const [targetResourceType, setTargetResourceType] = useState<string>(targetResourceTypes[0].id);
  const [namespaceOption, setNamespaceOption] = useState<string[]>([]);
  const [applications, setApplications] = useState<WorkloadObject[]>([]);
  const [expanded, setExpanded] = React.useState<string | false>(false);
  const [allTargetResource, setAllTargetResource] = useState<boolean>(false);
  const [checkedItems, setCheckedItems] = useState<CheckedItems>({});
  const [isLoadingApplications, setIsLoadingApplications] = useState<boolean>(false);

  useEffect(() => {
    getDropDownData();
  }, []);

  const getDropDownData = async () => {
    try {
      const response: any = await apiKubernetes.getK8sNamespaceNames(selectedCluster?.value);
      const namespaces = response?.data?.namespaces || [];
      setNamespaceOption(namespaces);
    } catch (error) {
      console.error(error);
    }
  };

  const handleChange = (panel: string) => (_event: React.SyntheticEvent, isExpanded: boolean) => {
    setExpanded(isExpanded ? panel : false);
  };

  const handleCheckboxChange = (app: WorkloadObject, isChecked: boolean) => {
    setCheckedItems((prevCheckedItems: { [key: string]: boolean }) => {
      const updatedCheckedItems = { ...prevCheckedItems, [app.name + app.namespace]: isChecked };
      const allChecked = Object.values(updatedCheckedItems).filter((value) => value === true).length;
      if (allChecked === applications?.length) {
        setAllTargetResource(true);
      } else {
        setAllTargetResource(false);
      }
      if (isChecked) {
        handleChildComponentChange(
          JSON.stringify([
            ...selectedApplications,
            {
              type: app.kind,
              name: app.name,
              namespace: app.namespace,
            },
          ]),
          'applications'
        );
        return { ...prevCheckedItems, [app.name + app.namespace]: isChecked };
      }
      handleChildComponentChange(
        JSON.stringify(selectedApplications.filter((ap) => ap.name !== app.name || ap.namespace !== app.namespace)),
        'applications'
      );
      return { ...prevCheckedItems, [app.name + app.namespace]: isChecked };
    });
  };

  const handleSelectAllChange = (isChecked: boolean) => {
    const newCheckedItems: { [key: string]: boolean } = {};
    applications.forEach((app) => {
      newCheckedItems[app.name + app.namespace] = isChecked;
    });
    setCheckedItems(newCheckedItems);
    setAllTargetResource(isChecked);
    if (isChecked) {
      handleChildComponentChange(
        JSON.stringify(applications.map((e) => ({ name: e.name, type: e.kind, namespace: e.namespace }))),
        'all-applications-check'
      );
    } else {
      handleChildComponentChange(JSON.stringify([]), 'all-applications-uncheck');
    }
  };

  useEffect(() => {
    if (Array.isArray(selectedNamespace)) {
      if (selectedNamespace.length > 0 && selectedCluster) {
        handleWorkloadList(selectedNamespace);
      } else {
        setApplications([]);
      }
    } else if (selectedNamespace && selectedCluster) {
      handleWorkloadList(selectedNamespace);
    }
  }, [selectedNamespace, JSON.stringify(selectedCluster)]);

  useEffect(() => {
    if (selectedApplications?.length) {
      const result: { [key: string]: boolean } = selectedApplications.reduce((obj: CheckedItems, item) => {
        obj[item.name + item.namespace] = true;
        return obj;
      }, {});
      setCheckedItems(result);
    } else {
      setCheckedItems({});
      setAllTargetResource(false);
    }
  }, [selectedApplications]);

  const handleWorkloadList = (namespace: string | string[]) => {
    const query = {
      accountId: selectedCluster.value,
      namespaceName: namespace,
      kind: ['Deployment', 'StatefulSet', 'Rollout', 'DaemonSet'],
    };
    setIsLoadingApplications(true);
    apiKubernetes
      .getAllK8sWorkload(query)
      .then((res) => {
        setApplications(res?.data);
      })
      .finally(() => {
        setIsLoadingApplications(false);
      });
  };

  const clusterValue = selectedCluster?.value || '';
  const clusterOptions = selectedCluster ? [{ value: selectedCluster.value, label: selectedCluster.label || selectedCluster.value }] : [];
  const namespaceMultiValue = Array.isArray(selectedNamespace) ? selectedNamespace : selectedNamespace ? [selectedNamespace] : [];
  const namespaceSingleValue = Array.isArray(selectedNamespace) ? selectedNamespace[0] || '' : selectedNamespace || '';

  return (
    <>
      {!hideTabs && <Tabs tabs={targetResourceTypes} value={targetResourceType} onChange={(next) => setTargetResourceType(next)} size='md' />}

      <Box sx={{ mt: 'var(--ds-space-4)' }}>
        <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', mb: 'var(--ds-space-4)' }}>
          <Box sx={{ minWidth: ds.space.mul(0, 115) }}>
            <Select
              id='select-cluster'
              label='Select Cluster'
              required
              value={clusterValue}
              options={clusterOptions}
              onChange={(next) => handleChildComponentChange(next, 'cluster')}
              disabled={!!selectedCluster || reviewRunbook || viewOnlyMode}
              minWidth={ds.space.mul(0, 115)}
              placeholder='Select cluster'
            />
          </Box>
          <Box sx={{ minWidth: ds.space.mul(0, 115) }}>
            {multipleNamespace ? (
              <Select
                id='select-namespace'
                label='Select Namespace'
                multiple
                value={namespaceMultiValue}
                options={namespaceOption}
                onChange={(next) => handleChildComponentChange(next, 'namespace')}
                disabled={reviewRunbook || viewOnlyMode}
                minWidth={ds.space.mul(0, 115)}
                placeholder='Select namespace(s)'
              />
            ) : (
              <Select
                id='select-namespace'
                label='Select Namespace'
                required
                value={namespaceSingleValue}
                options={namespaceOption}
                onChange={(next) => {
                  handleChildComponentChange(next, 'namespace');
                  setApplications([]);
                  setAllTargetResource(false);
                }}
                disabled={!selectedCluster || reviewRunbook || viewOnlyMode}
                minWidth={ds.space.mul(0, 115)}
                placeholder='Select namespace'
              />
            )}
          </Box>
        </Box>
        <Box>
          <Accordion
            id={'resource-selection-container'}
            className='gray-accordion'
            expanded={expanded === 'target-resources'}
            onChange={handleChange('target-resources')}
            sx={styles.accordion}
          >
            <AccordionSummary expandIcon={<ExpandMore />}>
              <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center' }}>
                <Box sx={{ mr: 'var(--ds-space-2)' }} onClick={(e) => e.stopPropagation()}>
                  <Checkbox
                    id='total-applications-checkbox'
                    size='sm'
                    checked={allTargetResource}
                    onChange={(next) => handleSelectAllChange(next)}
                    disabled={reviewRunbook || viewOnlyMode}
                    aria-label='Select all applications'
                  />
                </Box>
                <Typography sx={styles.grayLabel}>
                  Total Applications Selected - {Object.keys(checkedItems).filter((key) => checkedItems[key] == true).length}
                </Typography>
              </Box>
              <Box>{isLoadingApplications && <CircularProgress size={15} sx={{ ml: 'var(--ds-space-3)', color: 'var(--ds-gray-500)' }} />}</Box>
            </AccordionSummary>

            <AccordionDetails>
              {applications?.length > 0 && (
                <Box display='flex' flexDirection='column' gap={ds.space.mul(0, 5)}>
                  {applications.map((app, index) => (
                    <Box key={index} display='flex' alignItems='center' gap={ds.space.mul(0, 5)}>
                      <Checkbox
                        id={`${app.name}`}
                        size='sm'
                        checked={checkedItems[app.name + app.namespace] || false}
                        onChange={(next) => handleCheckboxChange(app, next)}
                        disabled={reviewRunbook || viewOnlyMode}
                        aria-label={app.name}
                      />
                      <Box>
                        <Box>
                          <Typography
                            sx={{ fontSize: 'var(--ds-text-body)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-brand-500)' }}
                          >
                            {app.name}
                          </Typography>
                        </Box>
                        <Box>
                          <Typography
                            sx={{ fontSize: 'var(--ds-text-caption)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-400)' }}
                          >
                            ns: {app.namespace} | cl: {selectedCluster?.label}
                          </Typography>
                        </Box>
                      </Box>
                    </Box>
                  ))}
                </Box>
              )}
            </AccordionDetails>
          </Accordion>
        </Box>
      </Box>
    </>
  );
};

export default RunbookTargetResource;

const styles = {
  lightBlueLabel: {
    padding: 'var(--ds-space-2) var(--ds-space-4)',
    fontSize: 'var(--ds-text-body-lg)',
    fontWeight: 'var(--ds-font-weight-semibold)',
    color: 'var(--ds-brand-500)',
    bgcolor: 'var(--ds-blue-100)',
    borderRadius: 'var(--ds-radius-sm)',
    flexGrow: 1,
    mb: 'var(--ds-space-4)',
  },

  numberWithHeading: {
    display: 'grid',
    gridTemplateColumns: '40px 1fr',
    gap: 'var(--ds-space-2)',

    '& .number-heading': {
      height: ds.space.mul(0, 20),
      width: ds.space.mul(0, 20),
      bgcolor: 'var(--ds-blue-300)',
      borderRadius: 'var(--ds-radius-sm)',
      fontSize: 'var(--ds-text-title)',
      fontWeight: 'var(--ds-font-weight-semibold)',
      color: 'var(--ds-brand-500)',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
    },

    '& .main-heading': {
      padding: 'var(--ds-space-2) var(--ds-space-4)',
      fontSize: 'var(--ds-text-body-lg)',
      fontWeight: 'var(--ds-font-weight-semibold)',
      color: 'var(--ds-brand-500)',
      bgcolor: 'var(--ds-blue-100)',
      borderRadius: 'var(--ds-radius-sm)',
      flexGrow: 1,
      height: ds.space.mul(0, 20),
      boxSizing: 'border-box',
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'space-between',
    },
  },
  grayLabel: {
    color: 'var(--ds-gray-600)',
    fontSize: 'var(--ds-text-small)',
    fontWeight: 'var(--ds-font-weight-medium)',
  },
  tabButton: {
    width: ds.space.mul(0, 90),
    padding: 'var(--ds-space-2) var(--ds-space-3)',
    fontSize: 'var(--ds-text-body-lg)',
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    textTransform: 'unset',
    borderRadius: 'var(--ds-radius-sm)',
    bgcolor: 'var(--ds-blue-100)',
    color: 'var(--ds-brand-500)',
    fontWeight: 'var(--ds-font-weight-regular)',
    gap: 'var(--ds-space-2)',

    '& img': {
      width: ds.space.mul(0, 7),
      height: ds.space.mul(0, 7),
      objectFit: 'contain',
    },

    '&.active': {
      bgcolor: 'var(--ds-brand-500)',
      color: 'white',
      fontWeight: 'var(--ds-font-weight-medium)',
    },
  },
  radioButtonsGroup: {
    fontFamily: 'inherit',
    '& .MuiFormControlLabel-label ': {
      fontSize: 'var(--ds-text-title)',
      fontFamily: 'inherit',
      fontWeight: 'var(--ds-font-weight-regular)',
      color: 'var(--ds-brand-500)',
      mr: 'var(--ds-space-6)',
    },
    '& .MuiRadio-root': {
      p: 'var(--ds-space-2)',
      '& svg': { width: ds.space[4], height: ds.space[4] },
    },
  },
  radioButtonsGroupSmall: {
    fontFamily: 'inherit',
    '& .MuiFormControlLabel-label ': {
      fontSize: 'var(--ds-text-body-lg)',
      fontFamily: 'inherit',
      fontWeight: 'var(--ds-font-weight-medium)',
      color: 'var(--ds-brand-500)',
      mr: 'var(--ds-space-6)',
    },
    '& .MuiRadio-root': {
      p: 'var(--ds-space-2)',
      '& svg': { width: ds.space[4], height: ds.space[4] },
    },
  },
  grid: {
    display: 'grid',
    gap: 'var(--ds-space-2)',
    gridTemplateColumns: '1fr 36px',
  },
  accordion: {
    border: 'none',
    boxShadow: 'none',
    '& .MuiAccordionSummary-root': {
      bgcolor: 'var(--ds-red-100)',
      fontSize: 'var(--ds-text-small)',
      fontWeight: 'var(--ds-font-weight-medium)',
      color: 'var(--ds-brand-500)',
      padding: 'var(--ds-space-2) var(--ds-space-4)',
      minHeight: 'unset',
      borderRadius: 'var(--ds-radius-sm)',
      border: '0.5px solid var(--ds-red-200)',

      '&.Mui-expanded': {
        minHeight: 'unset',
        borderRadius: 'var(--ds-radius-sm) var(--ds-radius-sm) 0px 0px',
      },

      '& .MuiAccordionSummary-content': {
        margin: '0px',
        padding: '0px',
      },
    },

    '&.gray-accordion': {
      '& .MuiAccordionSummary-root': {
        color: 'var(--ds-gray-600)',
        bgcolor: 'var(--ds-gray-100)',
        border: '0.5px solid var(--ds-gray-100)',
      },
    },

    '& .MuiAccordionDetails-root': {
      padding: 'var(--ds-space-3) var(--ds-space-5)',
      minHeight: 'unset',
      borderRadius: '0 0 var(--ds-radius-sm) var(--ds-radius-sm)',
      border: '0.5px solid var(--ds-red-200)',
      borderTop: 'none',
      color: 'var(--ds-gray-600)',
      fontSize: 'var(--ds-text-body-lg)',
    },
  },
};
