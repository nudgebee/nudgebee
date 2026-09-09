// Mirrors the Go-side dedup/close identity functions exactly:
// api-server/services/anomoly/service.go (anomalyFingerprint) and
// api-server/services/slo/service.go (sloFingerprint). Anomaly and SLO
// violation events set `events.finding_id` to this string, not the id of the
// `anomaly` / `slo_report` row that reported them (that id is fresh every
// detection cycle) — this is the only stable key that correlates a row shown
// in the UI to its event.
//
// Keep the format ("|"-joined, in this field order) identical to the Go
// functions. If either side changes, the other must change in the same PR or
// this correlation silently breaks again.

export function anomalyFingerprint(accountId: string, anomalyType: string, name: string, namespace: string): string {
  return `anomaly|${accountId}|${anomalyType}|${name}|${namespace}`;
}

export function sloFingerprint(accountId: string, configName: string, workloadName: string, namespace: string): string {
  return `slo|${accountId}|${configName}|${workloadName}|${namespace}`;
}
