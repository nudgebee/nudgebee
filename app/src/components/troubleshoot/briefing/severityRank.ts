export type SourceSeverity = 'HIGH' | 'MEDIUM' | 'LOW' | 'INFO' | 'DEBUG';
export type NubiRank = 'P0' | 'P1' | 'P2' | 'P3';

export const RANK_ORDER: NubiRank[] = ['P0', 'P1', 'P2', 'P3'];

export const SOURCE_SEVERITY_TO_RANK: Record<SourceSeverity, NubiRank> = {
  HIGH: 'P1',
  MEDIUM: 'P2',
  LOW: 'P3',
  INFO: 'P3',
  DEBUG: 'P3',
};

export const MAPPING_DISCLOSURE =
  'Assumes the mapping HIGH = P1 · MEDIUM = P2 · LOW/INFO = P3. This mapping is not defined in the product yet — it must be agreed before this column ships, because it decides every number above.';

export type Disagreement = 'below' | 'agreed' | 'above' | 'unscored';

export const classifyDisagreement = (sourceSeverity?: string | null, nubiRank?: string | null): Disagreement => {
  const expected = SOURCE_SEVERITY_TO_RANK[(sourceSeverity || '').toUpperCase() as SourceSeverity];
  const actualIndex = RANK_ORDER.indexOf((nubiRank || '').toUpperCase() as NubiRank);
  if (!expected || actualIndex === -1) return 'unscored';

  const expectedIndex = RANK_ORDER.indexOf(expected);
  if (actualIndex === expectedIndex) return 'agreed';
  return actualIndex > expectedIndex ? 'below' : 'above';
};
