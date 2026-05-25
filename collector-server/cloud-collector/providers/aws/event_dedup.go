package aws

import (
	"sync"
	"time"

	"nudgebee/collector/cloud/providers"
)

// ruleDeduper coalesces repeat executions of a single rule keyed by the
// rendered Fingerprint, with trailing-edge fire. The first event in a burst
// runs immediately; further events sharing the same fingerprint within the
// TTL window are suppressed but their latest payload is captured, and when
// the window expires the rule fires exactly once more against that latest
// payload. The captured event then starts a new TTL window — if more events
// arrive, another trailing fire happens; otherwise the entry is removed.
//
// Concurrency contract: Allow may be called from many goroutines. Each
// (rule, fingerprint) entry has its own mutex; entry removal uses a dead
// flag to close the race with concurrent callers who would otherwise write
// into a tombstoned entry.
//
// Context lifetime: the owner (TemplatedEventBridgeProcessor) must call
// bindContext once at startup with the long-lived consumer context. That
// context governs trailing-fire goroutine shutdown and the pCtx passed to
// onFire. If Allow runs before bindContext, dedup is a no-op (caller
// proceeds as if there was no deduper) — degrades gracefully rather than
// dropping events.
type ruleDeduper struct {
	ttl     time.Duration
	entries sync.Map
	onFire  func(pCtx providers.CloudProviderContext, ev EventBridgeEvent, acc providers.Account)
	pCtx    providers.CloudProviderContext
}

type dedupEntry struct {
	mu          sync.Mutex
	pending     bool
	dead        bool
	deadline    time.Time
	latestEvent EventBridgeEvent
	latestAcct  providers.Account
}

func newRuleDeduper(ttl time.Duration, onFire func(providers.CloudProviderContext, EventBridgeEvent, providers.Account)) *ruleDeduper {
	return &ruleDeduper{ttl: ttl, onFire: onFire}
}

// bindContext attaches the long-lived context used for trailing-fire
// goroutine shutdown and as the pCtx passed to onFire. Called exactly once
// by the processor at consumer startup; calling it twice overwrites.
func (d *ruleDeduper) bindContext(pCtx providers.CloudProviderContext) {
	d.pCtx = pCtx
}

// Allow reports whether the caller should execute the rule now. It returns
// false when the (rule, fingerprint) was already executed within the TTL
// window — the latest event/account is captured for the trailing fire and
// the caller MUST skip its own execution.
func (d *ruleDeduper) Allow(fingerprint string, ev EventBridgeEvent, acc providers.Account) bool {
	if d.pCtx == nil {
		return true
	}
	for {
		if v, loaded := d.entries.Load(fingerprint); loaded {
			entry := v.(*dedupEntry)
			entry.mu.Lock()
			if entry.dead {
				entry.mu.Unlock()
				// Trailing-fire goroutine has tombstoned and removed this
				// entry between our Load and Lock. Retry — Load will miss
				// and LoadOrStore will create a fresh entry.
				continue
			}
			entry.pending = true
			entry.latestEvent = ev
			entry.latestAcct = acc
			entry.mu.Unlock()
			return false
		}
		fresh := &dedupEntry{deadline: time.Now().Add(d.ttl)}
		if _, loaded := d.entries.LoadOrStore(fingerprint, fresh); !loaded {
			go d.trailingFire(fingerprint, fresh)
			return true
		}
		// Lost the LoadOrStore race; loop back to take the existing entry.
	}
}

func (d *ruleDeduper) trailingFire(fingerprint string, entry *dedupEntry) {
	// One reusable timer for the whole goroutine lifetime — avoids the
	// per-iteration allocation that `time.After` would otherwise produce.
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	done := d.pCtx.GetContext().Done()
	for {
		wait := time.Until(entry.deadline)
		if wait > 0 {
			if timer == nil {
				timer = time.NewTimer(wait)
			} else {
				timer.Reset(wait)
			}
			select {
			case <-done:
				if !timer.Stop() {
					// Timer already fired — drain the pending tick so it
					// doesn't leak into a future Reset (Go 1.23+ behavior
					// is forgiving here, but explicit drain is portable).
					select {
					case <-timer.C:
					default:
					}
				}
				entry.mu.Lock()
				entry.dead = true
				entry.mu.Unlock()
				d.entries.Delete(fingerprint)
				return
			case <-timer.C:
			}
		}
		entry.mu.Lock()
		if !entry.pending {
			entry.dead = true
			entry.mu.Unlock()
			d.entries.Delete(fingerprint)
			return
		}
		ev := entry.latestEvent
		acc := entry.latestAcct
		entry.pending = false
		entry.deadline = time.Now().Add(d.ttl)
		entry.mu.Unlock()
		d.onFire(d.pCtx, ev, acc)
	}
}
