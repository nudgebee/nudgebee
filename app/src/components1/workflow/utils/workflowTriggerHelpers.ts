interface InputSchemaItem {
  id: string;
  type: 'string' | 'int' | 'bool' | 'json' | 'array';
  description: string;
  default: any;
}

export const getDefaultTriggerInputs = (workflow: any): Record<string, any> => {
  const manualTrigger = workflow?.definition?.triggers?.find((trigger: any) => trigger.type === 'manual');
  if (manualTrigger?.params?.inputs) {
    return manualTrigger.params.inputs;
  }
  return {};
};

export const getWorkflowInputSchema = (workflow: any): InputSchemaItem[] => {
  const inputs = workflow?.definition?.inputs;
  if (!inputs || !Array.isArray(inputs)) {
    return [];
  }

  return inputs.map((input: any) => ({
    id: input.id,
    type: input.type,
    description: input.description || `Input parameter: ${input.id}`,
    default: input.default,
  }));
};

export const getPrimaryTriggerType = (workflow: any): string => {
  const triggers = workflow?.definition?.triggers;
  if (!triggers || triggers.length === 0) {
    return 'manual';
  }

  const manualTrigger = triggers.find((trigger: any) => trigger.type === 'manual');
  if (manualTrigger) {
    return 'manual';
  }

  return triggers[0].type;
};

export const hasManualTrigger = (workflow: any): boolean => {
  const triggers = workflow?.definition?.triggers;
  if (!triggers || !Array.isArray(triggers)) return false;
  return triggers.some((trigger: any) => trigger?.type === 'manual');
};
