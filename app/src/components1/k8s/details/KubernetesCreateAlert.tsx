import { Box, Stack, Typography } from '@mui/material';
import { DragIndicator, DeleteOutline as DeleteIconMui } from '@mui/icons-material';
import React, { useEffect, useState, useMemo } from 'react';
import CodeMirror from '@uiw/react-codemirror';
import { PromQLExtension } from '@prometheus-io/codemirror-promql';
import { isAlertNameValid, safeJSONParse } from 'src/utils/common';
import { hasFeatureAccess } from '@lib/auth';
import CheckIcon from '@mui/icons-material/Check';
import { Input } from '@components1/ds/Input';
import { Button as DsButton } from '@components1/ds/Button';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { Select } from '@components1/ds/Select';
import { toast as snackbar } from '@components1/ds/Toast';
import LinearLoader from '@components1/k8s/common/LinearLoader';
import DynamicForm from '@components1/common/DynamicForm';
import apiKubernetes1 from '@api1/kubernetes1';
import CustomAccordion from '@components1/common/CustomAccordion';
import { ds } from 'src/utils/colors';
import CustomStepper from '@components1/common/CustomStepper';
import apiAskNudgebee from '@api1/ask-nudgebee';
import observability from '@api1/observability';
import ReorderableList, { type DragHandleProps } from '@components1/common/ReorderableList';

interface KubernetesCreateAlertProps {
  accountId: string;
  handleCloseCreateNewAlertModal: () => void;
  onSubmit: (message: string, severity: string) => void;
  onClickLoader: (loaderStatus: boolean) => void;
  isCreateAlert: boolean;
  alertManagerObject?: any | null;
  agentPlaybookOnEvent?: any | [];
}

interface DisplayError {
  alertName: string;
  promQL: string;
  time: string;
  severity: string;
}

interface DropdownOption {
  label: string;
  value: string;
}

const KubernetesCreateAlert: React.FC<KubernetesCreateAlertProps> = ({
  accountId,
  alertManagerObject,
  handleCloseCreateNewAlertModal,
  onSubmit,
  onClickLoader,
  isCreateAlert,
  agentPlaybookOnEvent,
}) => {
  const [alertSummary, setAlertSummary] = useState<string>('');
  const [alertDescription, setAlertDescription] = useState<string>('');
  const [alertRunbook, setAlertRunbook] = useState<string>('');
  const [severity, setSeverity] = useState<string>('');
  const [alertName, setAlertName] = useState<string>('');
  const [promQL, setPromQL] = useState<string>('');
  const [time, setTime] = useState<string>('1'); // default time 1
  const [timeCondition, setTimeCondition] = useState<string>('m');
  const [source, setSource] = useState('prometheus');
  const [errorDesc, setErrorDesc] = useState<DisplayError>({
    alertName: '',
    promQL: '',
    time: '',
    severity: '',
  });
  const [isPromQLWrong, setIsPromQLWrong] = useState(true);
  const [loadingQueryExec, setLoadingQueryExec] = useState(false);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [formData, setFormData] = useState<any>({});
  const [actions, setActions] = useState<any>([]);
  const [selectedActions, setSelectedActions] = useState<Array<{ label: string; value: string; id: string }>>([]);
  const [actionsMap, setActionsMap] = useState<Record<string, any>>({});
  const [formErrors, setFormErrors] = useState<{ [actionName: string]: { [paramName: string]: string } }>({});
  const [loadingActions, setLoadingActions] = useState(false);
  const [expandedAccordions, setExpandedAccordions] = useState<Set<string>>(new Set());
  const [currentStep, setCurrentStep] = useState(1);
  const [stepErrors, setStepErrors] = useState<boolean[]>([false, false, false]);
  const [functions, setFunctions] = useState([]);

  // Memoized CodeMirror configuration to prevent unnecessary re-renders
  const codeMirrorExtensions = useMemo(() => [new PromQLExtension().asExtension()], []);
  const steps = ['Alert Configuration', 'Triggering Condition', 'Add Actions'];

  // Define custom button text for each step
  const nextButtonText = [
    'Next: Triggering Condition', // Step 1: Basic Configuration
    'Next: Add Actions', // Step 2: PromQL
    'Create Alert', // Step 3: Add Actions
  ];

  const submitButtonText = isCreateAlert ? 'Create Alert' : 'Update Alert';
  const backButtonText = 'Back';

  useEffect(() => {
    const fetchFunctions = async () => {
      try {
        const response = await apiAskNudgebee.listFunctions({ accountId });
        setFunctions((response as any).res?.llm_functions || []);
      } catch (error) {
        console.error('Error fetching functions:', error);
      }
    };
    hasFeatureAccess('LLM_FUNCTION').then((res) => {
      if (res) {
        fetchFunctions();
      }
    });
  }, []);

  const codeMirrorBasicSetup = useMemo(
    () => ({
      lineNumbers: true,
      foldGutter: true,
      dropCursor: false,
      allowMultipleSelections: false,
      indentOnInput: true,
      bracketMatching: true,
      closeBrackets: true,
      autocompletion: true,
      highlightSelectionMatches: false,
    }),
    []
  );

  const validateFormData = () => {
    const errors: any = {};

    selectedActions.forEach((action: any) => {
      const actionConfig = actionsMap[action.value];
      if (actionConfig?.params) {
        Object.entries(actionConfig.params).forEach(([paramName, paramConfig]: any) => {
          const fieldValue = formData[action.id]?.[paramName];
          const isArrayType = paramConfig.type === 'string[]' || paramConfig.type === 'object[]' || paramConfig.type === 'list'; // Include 'list' if it can be multi-select array

          if (paramConfig.required) {
            if (fieldValue === undefined || fieldValue === null || fieldValue === '') {
              if (!errors[action.id]) {
                errors[action.id] = {};
              }
              errors[action.id][paramName] = `${paramConfig.display_name || paramName} is required`;
            } else if (isArrayType && Array.isArray(fieldValue) && fieldValue.length === 0) {
              if (!errors[action.id]) {
                errors[action.id] = {};
              }
              errors[action.id][paramName] = `${paramConfig.display_name || paramName} is required`;
            }
          }
        });
      }
    });

    setFormErrors(errors);
    return Object.keys(errors).length === 0;
  };

  const parseDuration = (duration: string) => {
    if (duration) {
      const matches = duration.match(/^(\d+)([mh])$/);
      if (!matches || matches.length !== 3) {
        return { value: '', unit: 'm' };
      }
      const value = matches[1];
      const unit = matches[2];
      return { value, unit };
    }
    return { value: '', unit: 'm' };
  };

  const getActionListForPromQL = async (promQl: string) => {
    setLoadingActions(true);
    return apiKubernetes1
      .listActionPlaybookActions({
        cloud_account_id: accountId,
        query:
          alertManagerObject?.source == 'azure_monitor_webhook' ||
          alertManagerObject?.source == 'datadog_webhook' ||
          alertManagerObject?.source == 'chronosphere' ||
          alertManagerObject?.source == 'AWS_CloudWatch_Alarm'
            ? ''
            : promQl,
        source:
          alertManagerObject?.source == 'azure_monitor_webhook' ||
          alertManagerObject?.source == 'datadog_webhook' ||
          alertManagerObject?.source == 'AWS_CloudWatch_Alarm'
            ? 'nudgebee'
            : '',
      })
      .then(async (res) => {
        const actions = res?.data?.data?.alertmanager_list_actions?.actions || [];
        if (actions.length > 0) {
          const mappedActions = actions.reduce((acc: Record<string, any>, action: any) => {
            acc[action.name] = { ...action };
            return acc;
          }, {});

          const sortedActions = actions
            .sort((a: any, b: any) => a.display_name.localeCompare(b.display_name))
            .map((n: any) => ({ label: n.display_name, value: n.name, group: 'Actions' }));

          const functionOptions =
            functions
              .sort((a: any, b: any) => a.name.localeCompare(b.name))
              .map((n: any) => ({ label: n.name, value: n.name, group: 'LLM Functions' })) || [];

          const funcActions = functions.reduce((fu: Record<string, any>, func: any) => {
            fu[func.name] = {
              action_name: 'nubi_enricher',
              display_name: func.name,
              name: 'nubi_enricher',
              description: func.description,
            };
            return fu;
          }, {});

          const mergedData = [...sortedActions, ...functionOptions];
          setActions(mergedData);
          setActionsMap({ ...mappedActions, ...funcActions });
          return { actions: mergedData, actionsMap: mappedActions };
        }
        return { actions: [], actionsMap: {} };
      })
      .finally(() => {
        setLoadingActions(false);
      });
  };

  // Helper function to process existing alert data
  const processExistingAlertData = async (alertObj: any) => {
    setAlertName(alertObj.alert);
    const annotationParsed = (typeof alertObj.annotations === 'string' ? safeJSONParse(alertObj.annotations) : alertObj.annotations) || {};
    setAlertDescription(annotationParsed?.description || '');
    setAlertRunbook(annotationParsed?.runbook || '');
    setAlertSummary(annotationParsed?.summary || '');
    setSeverity(alertObj.severity);
    setSource(alertObj.source);
    setPromQL(alertObj.expr);

    const durationObj = parseDuration(alertObj.duration);
    setTime(durationObj.value || '1'); // default time 1
    setTimeCondition(durationObj.unit);
    setIsPromQLWrong(false);

    // First get the actions list
    const { actions: availableActions } = await getActionListForPromQL(alertObj.expr);

    // Then get the existing playbook data

    try {
      let agentPlaybooks = [];
      if (agentPlaybookOnEvent) {
        agentPlaybooks = agentPlaybookOnEvent;
      } else {
        const playbookRes = await apiKubernetes1.getAgentPlaybookOfEvent({
          accountId: accountId,
          alertName: alertObj.alert,
        });

        agentPlaybooks = playbookRes?.data?.data?.agent_playbook || [];
      }

      if (agentPlaybooks.length === 1 && availableActions.length > 0) {
        const playbook = agentPlaybooks[0];
        const actionParams = playbook.action_params || [];

        // Create selected actions with proper structure
        const processedSelectedActions: any[] = [];
        const processedFormData: any = {};

        const parsedActionParams = (typeof actionParams === 'string' ? safeJSONParse(actionParams) : actionParams) || [];
        parsedActionParams.forEach((paramObj: any, index: number) => {
          // Extract the action name (key) and its config (value)
          const actionName = Object.keys(paramObj)[0];
          const actionConfig = paramObj[actionName];

          // Find the corresponding action in available actions
          let matchingAction = availableActions.find((action: any) => action.value === actionName || action.value === actionConfig.ui_identifier);
          if (actionName == 'nubi_enricher' && functions.length) {
            const func = functions.find((f: any) => f.name === actionConfig.ui_identifier) as any;
            if (func) {
              matchingAction = {
                group: 'LLM Functions',
                label: func.name,
                value: func.name,
              };
            }
          }

          if (matchingAction) {
            // Create unique ID for this instance
            const uniqueId = `${matchingAction.value}_${Date.now()}_${index}`;

            // Add to selected actions with proper structure
            processedSelectedActions.push({
              ...matchingAction,
              id: uniqueId,
            });

            // Process form data - remove ui_identifier and instance_id from the actual form data
            const cleanedActionConfig = { ...actionConfig };
            delete cleanedActionConfig.ui_identifier;
            delete cleanedActionConfig.instance_id;

            processedFormData[uniqueId] = cleanedActionConfig;
          }
        });

        // Set the processed data
        setSelectedActions(processedSelectedActions);
        setFormData(processedFormData);
      }
    } catch (error) {
      console.error('Error loading existing playbook data:', error);
    }
  };

  useEffect(() => {
    if (isCreateAlert) {
      setSource('nudgebee');
    }

    if (alertManagerObject && Object.keys(alertManagerObject).length > 0) {
      processExistingAlertData(alertManagerObject);
      // When editing an existing alert, set current step to the last step
      // so all previous steps show as completed
      setCurrentStep(3);
    }
  }, [alertManagerObject, accountId]);

  // Separate useEffect for handling actions transformation (kept for any edge cases)
  useEffect(() => {
    if (actions.length > 0 && selectedActions.length > 0) {
      const isAlreadyTransformed = selectedActions.every((action: any) => typeof action === 'object' && action.value && action.label && action.id);

      if (!isAlreadyTransformed) {
        const selectedValues: any = selectedActions
          .map((action: any, index: number) => {
            const actionKey = typeof action === 'string' ? action : action.value;
            const foundAction: any = actions.find((a: any) => a.value === actionKey);

            if (foundAction) {
              return {
                ...foundAction,
                id: action.id || `${foundAction.value}_${Date.now()}_${index}`,
              };
            }
            return null;
          })
          .filter(Boolean);

        if (selectedValues.length > 0) {
          setSelectedActions(selectedValues);
        }
      }
    }
  }, [actions]);

  const clearFormError = (actionId: string, paramName: string) => {
    setFormErrors((prevErrors) => {
      const newErrors = { ...prevErrors };
      if (newErrors[actionId]) {
        delete newErrors[actionId][paramName];
        if (Object.keys(newErrors[actionId]).length === 0) {
          delete newErrors[actionId];
        }
      }
      return newErrors;
    });
  };

  const styles = {
    errorText: {
      color: ds.red[500],
      fontSize: 'var(--ds-text-small)',
      fontWeight: 'var(--ds-font-weight-medium)',
      mt: ds.space[2],
    },
    requiredStar: {
      color: ds.red[500],
      marginBottom: 'var(--ds-space-1)',
    },
    linkText: {
      color: ds.blue[500],
      textDecoration: 'underline',
      cursor: 'pointer',
      fontSize: 'var(--ds-text-body-lg)',
    },
    instructionText: {
      fontSize: 'var(--ds-text-small)',
      color: ds.gray[600],
      mb: 'var(--ds-space-2)',
    },
    promptContainer: {
      border: `1px solid ${ds.gray[200]}`,
      borderRadius: 'var(--ds-radius-lg)',
      minHeight: ds.space.mul(0, 100),
      padding: 'var(--ds-space-3)',
      backgroundColor: ds.background[100],
    },
    variableTable: {
      '& .MuiTableCell-root': {
        padding: 'var(--ds-space-2) var(--ds-space-3)',
        fontSize: 'var(--ds-text-body-lg)',
      },
      '& .MuiTableHead-root .MuiTableCell-root': {
        fontWeight: 'var(--ds-font-weight-semibold)',
        backgroundColor: 'var(--ds-background-200)',
      },
    },
  };

  const validateStep1 = () => {
    let hasError = false;
    const newErrorDesc = { ...errorDesc };

    if (source == 'chronosphere' || source == 'datadog_webhook') {
      hasError = false;
    } else if (!isAlertNameValid(alertName)) {
      newErrorDesc.alertName = 'Name should be number, letters and no spaces';
      hasError = true;
    } else {
      newErrorDesc.alertName = '';
    }

    if (!severity) {
      newErrorDesc.severity = 'Select severity';
      hasError = true;
    } else {
      newErrorDesc.severity = '';
    }

    setErrorDesc(newErrorDesc);
    return !hasError;
  };

  const validateStep2 = () => {
    let hasError = false;
    const newErrorDesc = { ...errorDesc };

    if (source == 'chronosphere' || source == 'datadog_webhook') {
      hasError = false;
    } else if (!time) {
      newErrorDesc.time = 'Time should greater than zero';
      hasError = true;
    } else if (!promQL) {
      newErrorDesc.promQL = 'Please enter correct prometheus query';
      hasError = true;
    } else if (isPromQLWrong) {
      newErrorDesc.promQL = 'Please enter correct/validate the prometheus query';
      hasError = true;
    } else {
      newErrorDesc.promQL = '';
      newErrorDesc.time = '';
    }

    setErrorDesc(newErrorDesc);
    return !hasError;
  };

  const validateStep3 = () => {
    const isFormDataValid = validateFormData();
    return isFormDataValid;
  };

  const validateCurrentStepAndShowErrors = () => {
    let isValid = false;
    const newStepErrors = [...stepErrors];

    if ((currentStep === 1 || currentStep === 2) && (source == 'chronosphere' || source == 'datadog_webhook')) {
      isValid = true;
    } else if (currentStep === 1) {
      isValid = validateStep1();
      newStepErrors[0] = !isValid;
    } else if (currentStep === 2) {
      isValid = validateStep2();
      newStepErrors[1] = !isValid;
    } else if (currentStep === 3) {
      isValid = validateStep3();
      newStepErrors[2] = !isValid;
    }

    setStepErrors(newStepErrors);
    return isValid;
  };

  const validateForm = () => {
    let _result = true;

    // Reset all errors first
    setErrorDesc({
      alertName: '',
      promQL: '',
      time: '',
      severity: '',
    });

    if (currentStep == 1) {
      _result = validateStep1();
    } else if (currentStep == 2) {
      _result = validateStep2();
    } else if (currentStep == 3) {
      _result = validateStep3();
    }

    return _result;
  };

  // Build action_params in the order shown to the user. Iterates
  // selectedActions (the visible order) rather than Object.keys(formData)
  // (insertion order) so reorder + delete/re-add cycles produce a payload
  // that matches the UI.
  const getRequestBodyActionParams = () => {
    const _result: Record<string, any>[] = [];
    for (const action of selectedActions) {
      const actionConfig = actionsMap[action.value];
      if (!actionConfig) continue;
      const actionName = actionConfig.action_name || action.value;
      _result.push({
        [actionName]: {
          ...(formData[action.id] || {}),
          ui_identifier: action.value,
          instance_id: action.id,
        },
      });
    }
    return _result;
  };

  const handleSubmit = () => {
    if (!validateForm()) {
      snackbar.error('Please fill all required fields.');

      const newExpandedSet = new Set(expandedAccordions);
      Object.keys(formErrors).forEach((actionId) => {
        if (formErrors[actionId] && Object.keys(formErrors[actionId]).length > 0) {
          newExpandedSet.add(actionId);
        }
      });

      if (
        newExpandedSet.size > expandedAccordions.size ||
        Object.keys(formErrors).some((id) => !expandedAccordions.has(id) && formErrors[id] && Object.keys(formErrors[id]).length > 0)
      ) {
        setExpandedAccordions(newExpandedSet);
      }
      return;
    }

    setIsSubmitting(true);
    const data: any = {
      annotations: {
        description: alertDescription,
        summary: alertSummary,
        ...(alertRunbook && { runbook: alertRunbook }),
      },
      expr: promQL,
      labels: {
        severity: severity,
      },
      alert: alertName,
      duration: time + timeCondition,
      accountId: accountId,
      source: source,
      category: 'kubernetes-apps',
      severity: severity.toLowerCase(),
      enabled: true,
      trigger_params: [
        {
          on_prometheus_alert: {
            status: 'firing',
            alert_name: `${alertName}`,
          },
        },
      ],
      action_params: getRequestBodyActionParams(),
    };

    const apiCall = isCreateAlert ? apiKubernetes1.createAlertManager(data) : apiKubernetes1.updateAlertManager(data);

    onClickLoader(true);
    apiCall
      .then((res: any) => {
        if (res?.data.errors || res?.data.errors?.length > 0 || res?.data?.data?.errors) {
          onSubmit(`Failed to ${isCreateAlert ? 'create' : 'update'} Alert Rule`, 'error');
        } else {
          onSubmit(`Rule ${alertName} ${isCreateAlert ? 'Created' : 'Updated'} Successfully`, 'success');
          handleCloseCreateNewAlertModal();
        }
      })
      .catch(() => {
        onSubmit(`Failed to ${isCreateAlert ? 'create' : 'update'} Alert Rule`, 'error');
      })
      .finally(() => {
        setIsSubmitting(false);
        onClickLoader(false);
      });
  };

  const handleTestClick = () => {
    setLoadingQueryExec(true);
    setLoadingActions(true);
    const requestBody = {
      account_id: accountId,
      queries: {
        promql_query: promQL,
      },
      start_time: new Date(new Date().getTime() - 5 * 60 * 1000).getTime(),
      end_time: new Date().getTime(),
      instant: true,
    };
    observability
      .metricsQuery(requestBody)
      .then((res) => {
        if (res?.data?.data?.metrics_list?.results?.[0]) {
          const result = res?.data?.data?.metrics_list?.results[0];
          if (result.error) {
            setErrorDesc((prevErrorDesc) => ({
              ...prevErrorDesc,
              promQL: 'Please enter correct prometheus query',
            }));
            setIsPromQLWrong(true);
          } else {
            setIsPromQLWrong(false);
            getActionListForPromQL(promQL);
          }
        } else {
          setErrorDesc((prevErrorDesc) => ({
            ...prevErrorDesc,
            promQL: 'Failed to execute prometheus query',
          }));
          snackbar.error('Failed to execute prometheus query');
          setLoadingActions(false);
        }
      })
      .finally(() => {
        setLoadingQueryExec(false);
      });
  };

  const handleFormChange = (updatedValue: any) => {
    setFormData((prev: any) => ({ ...prev, ...updatedValue }));
  };

  const filterActions = (name: string) => actionsMap[name] || {};

  const handleSingleActionSelect = (selectedOption: any) => {
    if (selectedOption) {
      const fullAction: any = actions.find((action: any) => action.value === selectedOption.value || action.value === selectedOption);

      if (fullAction) {
        const newAction = {
          ...fullAction,
          id: `${fullAction.value}_${Date.now()}_${Math.random()}`,
        };

        setSelectedActions((prev) => [...prev, newAction]);

        if (actionsMap[fullAction.value]?.params && selectedOption?.group !== 'LLM Functions') {
          const newFormData = Object.entries(actionsMap[fullAction.value].params).reduce((acc, [paramName, paramConfig]: any) => {
            if (paramConfig.type === 'bool') {
              acc[paramName] = paramConfig.default ?? false;
            } else if (paramConfig.type === 'list' || paramConfig.type === 'string[]') {
              acc[paramName] = paramConfig.default ?? [];
            } else {
              acc[paramName] = paramConfig.default || '';
            }
            return acc;
          }, {} as any);

          setFormData((prev: any) => ({
            ...prev,
            [newAction.id]: newFormData,
          }));
        } else if (selectedOption?.group === 'LLM Functions' && functions.length) {
          // For LLM functions, we don't need to set default params
          const func = (functions.filter((f: any) => f.name == selectedOption.value)?.[0] as any) || null;
          if (func) {
            setFormData((prev: any) => ({
              ...prev,
              [newAction.id]: {
                prompt: func.prompt,
              },
            }));
          }
        }
      }
    }
  };

  const handleDeleteAction = (deletedOption: string) => {
    const updatedSelectedActions = selectedActions.filter((action: any) => action.id !== deletedOption);
    setSelectedActions(updatedSelectedActions);
    const selectedValuesSet = new Set(updatedSelectedActions.map((option: any) => option.id));

    setFormData((prevFormData: any) => {
      const updatedFormData = Object.keys(prevFormData)
        .filter((key) => selectedValuesSet.has(key))
        .reduce((acc, key) => {
          acc[key] = prevFormData[key];
          return acc;
        }, {} as any);
      updatedSelectedActions.forEach((option: any) => {
        if (!updatedFormData[option.id] && actionsMap[option.value]?.params) {
          updatedFormData[option.id] = Object.entries(actionsMap[option.value].params).reduce((acc, [paramName, paramConfig]: any) => {
            if (paramConfig.type === 'bool') {
              acc[paramName] = paramConfig.default ?? false;
            } else if (paramConfig.type === 'list') {
              acc[paramName] = paramConfig.default ?? [];
            } else {
              acc[paramName] = paramConfig.default || '';
            }
            return acc;
          }, {} as any);
        }
      });

      return updatedFormData;
    });
  };

  const handleAccordionChange = (actionId: string) => (event: React.SyntheticEvent, isExpanded: boolean) => {
    setExpandedAccordions((prev) => {
      const newSet = new Set(prev);
      if (isExpanded) {
        newSet.add(actionId);
      } else {
        newSet.delete(actionId);
      }
      return newSet;
    });
  };

  const handleNext = () => {
    if (!validateCurrentStepAndShowErrors()) {
      snackbar.error('Please fill all required fields.');
      return;
    }

    setCurrentStep((prev) => prev + 1);
  };

  const handleBack = () => {
    setCurrentStep((prev) => prev - 1);
  };

  const handleStepChange = (step: number) => {
    // Validate current step before allowing navigation
    if (step > currentStep) {
      // Going forward - validate current step
      if (!validateCurrentStepAndShowErrors()) {
        snackbar.error('Please fill all required fields.');
        return;
      }
    } else if (step < currentStep) {
      // Going backward - validate all steps between current and target
      for (let i = currentStep; i > step; i--) {
        const newStepErrors = [...stepErrors];
        if (i === 1) {
          newStepErrors[0] = !validateStep1();
        } else if (i === 2) {
          newStepErrors[1] = !validateStep2();
        } else if (i === 3) {
          newStepErrors[2] = !validateStep3();
        }
        setStepErrors(newStepErrors);
      }
    }

    setCurrentStep(step);
  };

  const severityOptions: DropdownOption[] = [
    { label: 'Critical', value: 'critical' },
    { label: 'Warning', value: 'warning' },
  ];

  const timeConditionOptions: DropdownOption[] = [
    { label: 'minutes', value: 'm' },
    { label: 'hour', value: 'h' },
  ];

  return (
    <div style={{ display: 'flex', flexDirection: 'column' }}>
      {isSubmitting && (
        <Box sx={{ width: '100%', position: 'absolute', top: 0, left: 0 }}>
          <LinearLoader />
        </Box>
      )}
      <CustomStepper
        steps={steps}
        activeStep={currentStep}
        onNext={handleNext}
        onBack={handleBack}
        onStepChange={handleStepChange}
        onSubmit={handleSubmit}
        stepErrors={stepErrors}
        nextButtonText={nextButtonText}
        submitButtonText={submitButtonText}
        backButtonText={backButtonText}
        accountId={accountId}
        isSubmitting={isSubmitting}
      >
        {currentStep === 1 && (
          <Box
            sx={{
              p: 'var(--ds-space-4) var(--ds-space-7)', // reduced padding
              borderBottom: '1px solid var(--ds-blue-400)',
              minHeight: ds.space.mul(0, 175),
              display: 'flex',
              flexDirection: 'column',
            }}
          >
            <Stack spacing={2} sx={{ flex: 1 }}>
              {/* reduced spacing between sections */}
              <Box
                sx={{
                  p: 'var(--ds-space-3) var(--ds-space-4)',
                  backgroundColor: ds.blue[100],
                  borderRadius: 'var(--ds-radius-lg)',
                }}
              >
                <Typography sx={{ ...styles.instructionText, color: ds.gray[700], mb: '0px' }}>
                  1. Alerts can be configured based on TriggerConditions linked to Prometheus Metrics
                  <br />
                  2. you can also configure actions to be taken whenever the alert conditions are met
                </Typography>
              </Box>
              {/* Alert Name */}
              <Input
                label='Alert Name'
                instructionText='Choose a clear, descriptive name'
                placeholder='Enter Alert Name'
                value={alertName}
                onChange={(next) => {
                  if (!next.includes(' ')) {
                    setAlertName(next);
                    setErrorDesc((prev) => ({ ...prev, alertName: '' }));
                  } else {
                    setErrorDesc((prev) => ({ ...prev, alertName: 'No spaces allowed' }));
                  }
                }}
                error={errorDesc.alertName || undefined}
                disabled={!isCreateAlert || source == 'chronosphere' || source == 'datadog_webhook'}
              />
              {/* Alert Summary */}
              <Input
                label='Alert Summary'
                instructionText='Enter a brief, high-level explanation of what the alert is about'
                value={alertSummary}
                placeholder='Enter Alert Summary'
                onChange={(next) => setAlertSummary(next)}
                type='textarea'
                rows={2}
                disabled={source == 'chronosphere' || source == 'datadog_webhook'}
              />

              {/* Alert Description */}
              <Input
                label='Alert Description'
                instructionText='Enter a detailed description of the alert'
                placeholder='Enter Alert Description'
                value={alertDescription}
                onChange={(next) => setAlertDescription(next)}
                type='textarea'
                rows={5}
                disabled={source == 'chronosphere' || source == 'datadog_webhook'}
              />

              {/* Alert Runbooks */}
              <Input
                label='Runbook'
                instructionText='Add Runbook To Troubleshoot This Alert'
                placeholder='Enter Alert Runbook To Customize Troubleshooting'
                value={alertRunbook}
                onChange={(next) => setAlertRunbook(next)}
                type='textarea'
                rows={5}
                disabled={source == 'chronosphere' || source == 'datadog_webhook'}
              />

              {/* Severity */}
              <Box>
                <Select
                  id='alert-severity'
                  label='Severity'
                  required
                  value={severity}
                  options={severityOptions}
                  onChange={(next) => {
                    setSeverity(next);
                    setErrorDesc((prev) => ({ ...prev, severity: '' }));
                  }}
                  error={errorDesc.severity || undefined}
                  disabled={source == 'chronosphere' || source == 'datadog_webhook'}
                />
              </Box>
            </Stack>
          </Box>
        )}

        {currentStep === 2 && (
          <Box
            sx={{
              p: 'var(--ds-space-4) var(--ds-space-7)', // reduced padding
              borderBottom: '1px solid var(--ds-blue-400)',
              minHeight: ds.space.mul(0, 175),
              display: 'flex',
              flexDirection: 'column',
            }}
          >
            <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-4)', flex: 1 }}>
              <Box
                sx={{
                  p: 'var(--ds-space-3) var(--ds-space-4)',
                  backgroundColor: ds.blue[100],
                  borderRadius: 'var(--ds-radius-lg)',
                }}
              >
                <Typography sx={{ ...styles.instructionText, color: ds.gray[700], mb: '0px' }}>
                  1. Alerts can be configured based on TriggerConditions linked to Prometheus Metrics (others coming soon.)
                  <br />
                  2. Triggers Conditions are based on
                  <br />
                  <Box sx={{ pl: ds.space[2] }}>
                    • Prometheus Query gets evaluated to true
                    <br />• it stays true for time period as specified
                  </Box>
                </Typography>
              </Box>
              {/* PromQL Input Section */}
              <Box>
                <Typography sx={{ mb: '0px', fontSize: 'var(--ds-text-body-lg)', fontWeight: 'var(--ds-font-weight-medium)', color: ds.gray[700] }}>
                  PromQL
                </Typography>
                <Typography sx={{ ...styles.instructionText, color: ds.gray[700], mb: 'var(--ds-space-2)' }}>
                  PromQL (Prometheus Query Language) is used to define the condition that will trigger this alert.
                </Typography>
                <CodeMirror
                  value={promQL}
                  height='120px'
                  extensions={codeMirrorExtensions}
                  onChange={(_value) => {
                    setPromQL(_value);
                    setErrorDesc((prevErrorDesc) => ({
                      ...prevErrorDesc,
                      promQL: '',
                    }));
                    setIsPromQLWrong(true);
                  }}
                  editable={source != 'chronosphere' && source != 'datadog_webhook'}
                  theme='light'
                  basicSetup={codeMirrorBasicSetup}
                  style={{
                    width: '100%',
                    overflow: 'hidden', // Prevent horizontal scroll
                    whiteSpace: 'pre-wrap', // Allow line wrap
                    wordBreak: 'break-word', // Break long tokens
                    fontSize: 'var(--ds-text-body-lg)',
                    borderRadius: 'var(--ds-radius-sm)',
                    border: '1px solid var(--ds-brand-200)',
                  }}
                />
                {errorDesc.promQL.length > 0 && (
                  <Typography sx={{ color: 'red', fontSize: 'var(--ds-text-body-lg)', mt: ds.space[2] }}>{errorDesc.promQL}</Typography>
                )}
                {/* Validate Button & Success Indicator */}
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[4], mt: 'var(--ds-space-3)' }}>
                  <DsButton
                    tone='primary'
                    size='md'
                    disabled={loadingQueryExec || source == 'chronosphere' || source == 'datadog_webhook'}
                    loading={loadingQueryExec}
                    onClick={() => {
                      setErrorDesc((prevErrorDesc) => ({
                        ...prevErrorDesc,
                        promQL: '',
                      }));
                      setIsPromQLWrong(true);
                      handleTestClick();
                    }}
                  >
                    Validate Query
                  </DsButton>
                  {(!isPromQLWrong || source == 'chronosphere' || source == 'datadog_webhook') && (
                    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                      <CheckIcon sx={{ color: 'var(--ds-teal-500)', fontSize: 20 }} />
                      <Typography sx={{ color: 'var(--ds-teal-500)', fontSize: 14, fontWeight: 'var(--ds-font-weight-medium)' }}>
                        Query validated successfully
                      </Typography>
                    </Box>
                  )}
                </Box>
              </Box>

              <Box sx={{ display: 'flex', gap: 'var(--ds-space-3)', alignItems: 'flex-start', mt: 'var(--ds-space-4)' }}>
                {/* Define Time */}
                <Box sx={{ width: ds.space.mul(0, 80), flexShrink: 0 }}>
                  <Input
                    label='Define Time'
                    placeholder='Enter Time'
                    type='number'
                    inputMode='numeric'
                    value={time}
                    onChange={(next) => {
                      // Legacy `inputProps={{ min: 1 }}` was dropped during DS migration — DS Input has
                      // no `min` pass-through, so enforce min=1 at the React level: strip non-digits,
                      // then drop a lone '0'.
                      const stripped = next.replace(/\D/g, '');
                      const clamped = stripped === '0' ? '' : stripped;
                      setTime(clamped);
                      setErrorDesc((prev) => ({ ...prev, time: '' }));
                    }}
                    error={errorDesc.time || undefined}
                    disabled={source == 'chronosphere' || source == 'datadog_webhook'}
                  />
                </Box>

                {/* Time Condition */}
                <Box sx={{ width: ds.space.mul(0, 80), flexShrink: 0 }}>
                  <Select
                    id='alert-time-condition'
                    label='Time Condition'
                    required
                    value={timeCondition}
                    options={timeConditionOptions}
                    onChange={(next) => setTimeCondition(next)}
                    disabled={source == 'chronosphere' || source == 'datadog_webhook'}
                  />
                </Box>
              </Box>
            </Box>
          </Box>
        )}

        {currentStep === 3 && (
          <>
            <Box
              sx={{
                p: 'var(--ds-space-4) var(--ds-space-7)',
                borderBottom: '1px solid var(--ds-blue-400)',
                minHeight: ds.space.mul(0, 175),
                display: 'flex',
                flexDirection: 'column',
              }}
            >
              <Box
                sx={{
                  p: 'var(--ds-space-3) var(--ds-space-4)',
                  backgroundColor: ds.blue[100],
                  borderRadius: 'var(--ds-radius-lg)',
                }}
              >
                <Typography sx={{ ...styles.instructionText, color: ds.gray[700], mb: '0px' }}>
                  1. you can configure Actions to be taken when an alert condition is met
                  <br />
                  2. Actions can be:
                  <br />
                  <Box sx={{ pl: ds.space[2] }}>
                    • list of functions.
                    <br />• list of your custom prompt based functions.
                  </Box>
                </Typography>
              </Box>
              {/* Top row: Dropdown on left, Count on right */}
              <Box
                sx={{
                  display: 'flex',
                  justifyContent: 'space-between',
                  alignItems: 'center',
                  flexWrap: 'wrap',
                  gap: 'var(--ds-space-3)',
                  mb: 'var(--ds-space-5)',
                  mt: 'var(--ds-space-4)',
                }}
              >
                {/* Dropdown */}
                <Box sx={{ flexShrink: 1, minWidth: 0, maxWidth: ds.space.mul(0, 140), width: '100%' }}>
                  <FilterDropdown
                    id='alert-action-picker'
                    options={actions || []}
                    value={null}
                    onSelect={(_e, selectedValue) => {
                      if (selectedValue) {
                        handleSingleActionSelect(selectedValue);
                      }
                    }}
                    label='Select Action to Add'
                    isOptionsLoading={loadingActions}
                  />
                </Box>

                {/* Actions Selected Count */}
                {selectedActions.length > 0 && (
                  <Typography
                    sx={{
                      fontWeight: 'var(--ds-font-weight-medium)',
                      color: 'var(--ds-brand-500)',
                      fontSize: 'var(--ds-text-body-lg)',
                      whiteSpace: 'nowrap',
                      flexShrink: 0,
                    }}
                  >
                    {selectedActions.length} Action{selectedActions.length > 1 ? 's' : ''} Selected
                  </Typography>
                )}
              </Box>

              {/* Selected Actions Accordions */}
              {selectedActions.length > 0 && (
                <Box sx={{ maxWidth: '100%', overflowX: 'hidden' }}>
                  <ReorderableList
                    items={selectedActions}
                    getItemKey={(actionInstance) => actionInstance.id}
                    onReorder={(next) => setSelectedActions(next)}
                    getDragLabel={(actionInstance) => {
                      const details = filterActions(actionInstance.value);
                      return formData[actionInstance.id]?.title || details?.display_name || actionInstance.label || 'Action';
                    }}
                    renderItem={(actionInstance: any, _index: number, dragHandleProps: DragHandleProps) => {
                      const actionDetails = filterActions(actionInstance.value);
                      if (!actionDetails) return null;
                      return (
                        <Box
                          sx={{
                            display: 'flex',
                            gap: 'var(--ds-space-3)',
                            alignItems: 'center',
                            width: '100%',
                            boxSizing: 'border-box',
                            overflowX: 'hidden',
                            mb: 'var(--ds-space-4)',
                          }}
                        >
                          <Box
                            {...dragHandleProps}
                            sx={{
                              display: 'flex',
                              alignItems: 'center',
                              color: 'var(--ds-gray-400)',
                              '&:hover': { color: 'var(--ds-brand-500)' },
                            }}
                            title='Drag to reorder'
                          >
                            <DragIndicator sx={{ fontSize: 20 }} />
                          </Box>
                          <Box flexGrow={1} sx={{ minWidth: 0 }}>
                            <CustomAccordion
                              title={formData[actionInstance.id]?.title || actionDetails.display_name || ''}
                              description={actionDetails.description || ''}
                              icon={actionDetails.icon || null}
                              expanded={expandedAccordions.has(actionInstance.id)}
                              onChange={handleAccordionChange(actionInstance.id)}
                            >
                              <Box
                                sx={{
                                  p: 'var(--ds-space-3) 0px',
                                  display: 'flex',
                                  flexDirection: 'column',
                                  gap: 'var(--ds-space-4)',
                                  maxWidth: '100%',
                                  overflowX: 'hidden',
                                }}
                              >
                                <DynamicForm
                                  actionKey={actionInstance.id}
                                  actionDetails={actionDetails}
                                  onChange={handleFormChange}
                                  errors={formErrors[actionInstance.id]}
                                  initialValues={formData[actionInstance.id]}
                                  accountId={accountId}
                                  onClearError={(paramName) => clearFormError(actionInstance.id, paramName)}
                                />
                              </Box>
                            </CustomAccordion>
                          </Box>
                          <DsButton
                            tone='danger'
                            size='sm'
                            composition='icon-only'
                            aria-label='Delete action'
                            icon={<DeleteIconMui fontSize='small' />}
                            onClick={() => handleDeleteAction(actionInstance.id)}
                          />
                        </Box>
                      );
                    }}
                  />
                </Box>
              )}
            </Box>
          </>
        )}
      </CustomStepper>
    </div>
  );
};

export default KubernetesCreateAlert;
