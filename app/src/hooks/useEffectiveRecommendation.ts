import { useState, useEffect, useMemo } from 'react';
import { safeParseJSON } from '@components/optimise-new/utils';

export interface AlternateInstanceOption {
  label: string;
  value: string;
}

export function useEffectiveRecommendation(recommendation: any) {
  const isAlternateInstancesRule = recommendation?.rule_name === 'aws_ec2_alternate_instances';
  const recData = safeParseJSON(recommendation?.recommendation);
  const alternateInstances: any[] = isAlternateInstancesRule ? recData?.alternate_instances ?? [] : [];

  const [selectedAlternateType, setSelectedAlternateType] = useState<string>(alternateInstances[0]?.instanceType ?? '');

  useEffect(() => {
    setSelectedAlternateType(alternateInstances[0]?.instanceType ?? '');
  }, [recommendation?.id, alternateInstances[0]?.instanceType]);

  const effectiveRecommendation = useMemo(() => {
    const base = { ...recommendation, recommendation: recData };
    if (!selectedAlternateType || !alternateInstances.length || !recData) return base;
    const selected = alternateInstances.find((i: any) => i.instanceType === selectedAlternateType);
    if (!selected) return base;
    return {
      ...base,
      recommendation: {
        ...recData,
        alternate_instances: [selected, ...alternateInstances.filter((i: any) => i.instanceType !== selectedAlternateType)],
      },
    };
  }, [recommendation, recData, selectedAlternateType, alternateInstances]);

  const alternateOptions: AlternateInstanceOption[] = alternateInstances.map((i: any) => ({
    label: i.price != null ? `${i.instanceType} — $${i.price}/hr` : i.instanceType,
    value: i.instanceType,
  }));

  return { alternateOptions, selectedAlternateType, setSelectedAlternateType, effectiveRecommendation };
}
