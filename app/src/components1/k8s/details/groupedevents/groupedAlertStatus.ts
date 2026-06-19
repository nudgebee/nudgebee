export const getGroupedAlertStatus = (distinctStatus?: string): 'FIRING' | 'CLOSED' =>
  distinctStatus?.includes('FIRING') ? 'FIRING' : 'CLOSED';
