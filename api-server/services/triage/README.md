# Event Triage System

## Overview

The event triage system provides intelligent analysis of alert instances with:
- **Duplicate detection**: Tracks duplicate events with same fingerprint
- **Event correlation**: Correlates related events using service dependencies and temporal proximity
- **Historical analytics**: On-demand 30-day statistics and 24-hour trends

## Processing Model

### Real-time Processing
When a new event is created, `triage.ProcessEvent()` is called automatically to:
1. Detect duplicates with same fingerprint
2. Correlate with related events in 10-minute window
3. Store results in `event_duplicates` and `event_correlations` tables

**Limitation**: Real-time processing only looks backward once, so a single event might show `occurrence_number=2` even if 100+ duplicates exist historically.

### Backfill Processing
To build complete duplicate chains for historical events, use the backfill API:

```bash
POST /api/events/backfill-triage
Content-Type: application/json
X-ACTION-TOKEN: <your-token>

{
  "cloud_account_id": "uuid-of-cloud-account",
  "fingerprint": "optional-specific-fingerprint",
  "start_time": "2025-12-08T00:00:00Z",
  "end_time": "2025-12-09T00:00:00Z",
  "dry_run": false
}
```

**Note**: The `batch_size` parameter is optional (reserved for future use). All events are currently processed together to ensure continuous duplicate chains.

**How it works**:
1. Fetches all events matching filters in chronological order (`ORDER BY starts_at ASC, created_at ASC`)
2. Clears existing triage data for those events
3. Groups events by fingerprint and builds complete duplicate chains in memory (no database queries)
4. Processes all events together (not in batches) to ensure continuous chains
5. Inserts complete duplicate chains with sequential occurrence numbers (1, 2, 3, ...)
6. Returns statistics: total events, duplicates detected, correlations created

**Key difference from real-time processing**:
- Real-time: Queries database for previous event → Only looks backward once
- Backfill: Builds chains in memory → Processes all events sequentially → Correct occurrence numbers

**Use cases**:
- Initial setup to populate triage data for existing events
- After schema changes or bug fixes to rebuild triage data
- For specific fingerprints showing incomplete duplicate chains

## API Endpoints

### Get Duplicate Chain
```
GET /api/events/{event_id}/duplicate-chain
```

Returns all duplicates for an event in chronological order with occurrence numbers.

### Get Correlated Events
```
GET /api/events/{event_id}/correlations
```

Returns events correlated to this event with correlation scores and reasons.

### Get Historical Stats
```
GET /api/events/fingerprint/{fingerprint}/stats?cloud_account_id={uuid}
```

Returns 30-day statistics:
- Total events, firing/resolved/closed counts
- Resolution and closure rates
- Noise level
- Average duration
- First/last seen timestamps

### Get Hourly Trend
```
GET /api/events/fingerprint/{fingerprint}/trend?cloud_account_id={uuid}
```

Returns hourly event counts for the last 24 hours.

### Deduplicate Correlations
```
POST /api/events/deduplicate-correlations
Content-Type: application/json
X-ACTION-TOKEN: <your-token>

{
  "cloud_account_id": "uuid-of-cloud-account"
}
```

Removes duplicate correlations where the same event correlates multiple times to events with the same fingerprint. Keeps only the **highest scoring** correlation per (event_id, fingerprint) pair (or earliest occurrence if scores are equal). This is useful for cleaning up correlation data that was created before the DISTINCT ON fix was applied.

## Correlation Algorithm

Events are correlated using a multi-factor scoring algorithm with threshold 0.50.

**Important**: Correlations are deduplicated by fingerprint - each event correlates to only **ONE representative event** per unique fingerprint, not all duplicates. This prevents one event from correlating with 50+ duplicates of another alert type.

### Factors
1. **Temporal proximity** (0.05-0.30):
   - Within 2 minutes: +0.30
   - Within 5 minutes: +0.15
   - Within 10 minutes: +0.05

2. **Service dependency distance** (0.05-0.40):
   - Direct dependency: +0.40
   - 2 hops: +0.25
   - 3 hops: +0.15
   - 4 hops: +0.05

3. **Bonuses**:
   - Causality (downstream after upstream): +0.15
   - Same namespace: +0.10
   - Same service: +0.15

### Examples

**Correlated** (score ≥ 0.50):
- Same namespace + direct dependency + 2min apart = 0.10 + 0.40 + 0.30 = 0.80
- Different namespace + 2-hop dependency + causality = 0.25 + 0.15 + 0.05 = 0.45 ❌
- Same namespace + 2-hop dependency + 5min apart = 0.10 + 0.25 + 0.15 = 0.50 ✓

**Not correlated** (score < 0.50):
- Time proximity only: 0.30 (needs additional signals)
- No service dependency graph data: correlation unlikely

## Testing

### Run triage on specific event
```bash
go test -v ./api -run TestProcessEventTriage
```

### Run backfill test
```bash
export TEST_TENANT="your-tenant-id"
export TEST_ACCOUNT_ID="your-cloud-account-id"
go test -v ./api -run TestBackfillTriage
```

The backfill test verifies:
- Complete duplicate chains with sequential occurrence numbers
- All duplicates point to same first_event_id
- Historical stats compute correctly after backfill

## Database Schema

### event_duplicates
- `event_id` (PK): Current event
- `fingerprint`: Event fingerprint
- `cloud_account_id` (PK): Cloud account
- `tenant_id`: Tenant ID
- `first_event_id`: First event in chain
- `previous_event_id`: Previous duplicate
- `occurrence_number`: Position in chain (1-based)
- `time_since_first_seconds`: Seconds since first occurrence
- `time_since_previous_seconds`: Seconds since previous occurrence
- Unique constraint: `(event_id, cloud_account_id)`

### event_correlations
- `event_id` (PK): First event
- `related_event_id` (PK): Related event
- `cloud_account_id` (PK): Cloud account
- `tenant_id`: Tenant ID
- `correlation_type`: Type of correlation
- `correlation_score`: Score (0.0-1.0)
- `correlation_reason`: Human-readable reason
- `time_offset_minutes`: Time difference
- `dependency_distance`: Hops in service graph
- Unique constraint: `(related_event_id, event_id, cloud_account_id)`

**Note**: Correlations stored bidirectionally for efficient queries.

## Performance

- Real-time processing: ~3800ms per event (within acceptable range)
- Backfill: Batch processing in configurable batch sizes (default: 100)
- Analytics: On-demand computation (no caching)
- Target: <200ms for simple queries
