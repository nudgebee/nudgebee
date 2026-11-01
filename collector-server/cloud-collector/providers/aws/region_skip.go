package aws

import (
	"log/slog"
	"sync"
	"time"
)

// regionSkipKind says why a failed call means a region should be skipped this time
// rather than reported as an error.
type regionSkipKind int

const (
	regionSkipNone regionSkipKind = iota
	regionSkipEndpointMissing
	regionSkipServiceUnavailable
	regionSkipUnreachable
	regionSkipDisabled
	regionSkipThrottled
)

// classifyRegionSkip is the one place that decides whether a regional AWS call
// failed in a way that means "skip this region" rather than "report an error". Every
// caller that loops over an account's regions uses it, so a new caller inherits the
// same rules instead of growing its own copy — the copies are how alarm polling was
// left without the unreachable-region skip the resource and alarm-rule syncs had.
func classifyRegionSkip(err error) regionSkipKind {
	switch {
	case err == nil:
		return regionSkipNone
	case isRegionEndpointMissing(err):
		return regionSkipEndpointMissing
	case isServiceUnavailableInRegion(err):
		return regionSkipServiceUnavailable
	case isRegionUnreachable(err), isGatewayFailureWithoutRequestID(err):
		return regionSkipUnreachable
	case isRegionDisabled(err):
		return regionSkipDisabled
	case isThrottleError(err):
		return regionSkipThrottled
	}
	return regionSkipNone
}

// logRegionSkip logs a skipped region once, at the level its kind deserves: an
// endpoint that does not exist is expected (debug), an unreachable or rate-limited
// region is worth a warning, and the rest are informational. attrs are extra
// key-value pairs, such as the service and account.
func logRegionSkip(logger *slog.Logger, kind regionSkipKind, err error, region string, attrs ...any) {
	attrs = append([]any{"region", region}, attrs...)
	switch kind {
	case regionSkipEndpointMissing:
		logger.Debug("skipping region without service endpoint", attrs...)
	case regionSkipServiceUnavailable:
		logger.Info("skipping region where service rejects the request as unavailable", append(attrs, "error", err)...)
	case regionSkipUnreachable:
		logger.Warn("skipping unreachable region endpoint", append(attrs, "error", err)...)
	case regionSkipDisabled:
		logger.Info("skipping region not enabled for this account", append(attrs, "error", err)...)
	case regionSkipThrottled:
		logger.Warn("skipping rate-limited region; will retry next sync", append(attrs, "error", err)...)
	}
}

// skipRegion classifies err and, when it means the region should be skipped, logs
// that once and returns true. A false return means err is a real failure the caller
// must report.
func skipRegion(logger *slog.Logger, err error, region string, attrs ...any) bool {
	kind := classifyRegionSkip(err)
	if kind == regionSkipNone {
		return false
	}
	logRegionSkip(logger, kind, err, region, attrs...)
	return true
}

// regionBackoff remembers regions an account could not reach, so a caller that runs
// often does not wait for the same connection timeouts on every run. A region is set
// aside only after it has failed failuresBeforeBackoff times in a row — a single
// network blip in a healthy region must not silence it — and only for cooldown;
// after that it is tried again, so a region that recovers is picked up without a
// restart. Any success clears it. The memory is per process.
type regionBackoff struct {
	mu                    sync.Mutex
	failuresBeforeBackoff int
	cooldown              time.Duration
	now                   func() time.Time
	entries               map[string]*regionBackoffEntry
}

type regionBackoffEntry struct {
	consecutiveFailures int
	retryAfter          time.Time
}

func newRegionBackoff(failuresBeforeBackoff int, cooldown time.Duration) *regionBackoff {
	return &regionBackoff{
		failuresBeforeBackoff: failuresBeforeBackoff,
		cooldown:              cooldown,
		now:                   time.Now,
		entries:               map[string]*regionBackoffEntry{},
	}
}

func regionBackoffKey(accountNumber, region string) string {
	return accountNumber + "/" + region
}

// shouldSkip reports whether the region is set aside for the account right now.
func (b *regionBackoff) shouldSkip(accountNumber, region string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	entry, ok := b.entries[regionBackoffKey(accountNumber, region)]
	return ok && b.now().Before(entry.retryAfter)
}

// recordUnreachable notes a failed connection to the region and reports whether it
// is now set aside.
func (b *regionBackoff) recordUnreachable(accountNumber, region string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := regionBackoffKey(accountNumber, region)
	entry, ok := b.entries[key]
	if !ok {
		entry = &regionBackoffEntry{}
		b.entries[key] = entry
	}
	entry.consecutiveFailures++
	if entry.consecutiveFailures >= b.failuresBeforeBackoff {
		entry.retryAfter = b.now().Add(b.cooldown)
		return true
	}
	return false
}

// recordSuccess clears anything remembered about the region.
func (b *regionBackoff) recordSuccess(accountNumber, region string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, regionBackoffKey(accountNumber, region))
}

const (
	// alarmPollUnreachableFailures and alarmPollUnreachableCooldown bound how alarm
	// polling, which runs every ten minutes, treats a region it cannot connect to:
	// two failed polls in a row (each already several attempts by the SDK) set the
	// region aside for half an hour.
	alarmPollUnreachableFailures = 2
	alarmPollUnreachableCooldown = 30 * time.Minute
)

// alarmPollBackoff is the unreachable-region memory of alarm polling.
var alarmPollBackoff = newRegionBackoff(alarmPollUnreachableFailures, alarmPollUnreachableCooldown)

const (
	// resourceListUnreachableFailures and resourceListUnreachableCooldown bound how
	// resource listing treats a region it cannot connect to. One account sync lists
	// every service in turn, and each service paid the full connection timeout
	// (~40s with the SDK's attempts) in a region the cluster cannot reach. The memory
	// is per account and region, not per service, so two services failing to connect
	// in a row set the region aside for the services that follow. A region where some
	// service still answers is cleared by that success.
	resourceListUnreachableFailures = 2
	resourceListUnreachableCooldown = 30 * time.Minute
)

// resourceListBackoff is the unreachable-region memory of resource listing.
var resourceListBackoff = newRegionBackoff(resourceListUnreachableFailures, resourceListUnreachableCooldown)
