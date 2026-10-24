package api

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"nudgebee/llm/security"
)

// The remediation leader jobs (follow-ups, planning) move rows of every tenant on in one tick, and a step can wait on
// a customer's cluster through the relay (the follow-up's verdict reads the pod). tickScheduler keeps one slow
// cluster from stalling every other tenant: tenants take turns (round-robin, the lead moving on each tick), each gets
// at most perTenant steps a tick, and a step that runs past its deadline is left running in the background — the
// tick moves on, skips that tenant's other steps for the rest of the tick, and does not start the overrunning row
// again until it returns.
type tickScheduler struct {
	name string
	mu   sync.Mutex
	// inflight are the rows whose step is still running from an earlier tick.
	inflight map[string]bool
	// lead is the tenant that went first last tick; the next one goes first now.
	lead string
}

func newTickScheduler(name string) *tickScheduler {
	return &tickScheduler{name: name, inflight: map[string]bool{}}
}

// tickTenantOf is the tenant an account belongs to, or the account itself when it cannot be read (it then forms its
// own turn). A var so tests can stub it.
var tickTenantOf = func(accountId string) string {
	if tenant, err := security.GetTenantIdFromAccountId(accountId); err == nil && tenant != "" {
		return tenant
	}
	return "account:" + accountId
}

// tickItem is one row a tick may move on.
type tickItem struct {
	Key       string // the row, for the in-flight check
	AccountId string
	Run       func()
}

// order is items in the order the tick takes them: tenants round-robin, starting after last tick's lead, at most
// perTenant each, in the order each tenant's rows came.
func (s *tickScheduler) order(items []tickItem, perTenant int) []tickItem {
	byTenant := map[string][]tickItem{}
	var tenants []string
	for _, it := range items {
		t := tickTenantOf(it.AccountId)
		if _, seen := byTenant[t]; !seen {
			tenants = append(tenants, t)
		}
		byTenant[t] = append(byTenant[t], it)
	}
	if len(tenants) == 0 {
		return nil
	}
	s.mu.Lock()
	start := 0
	for i, t := range tenants {
		if t == s.lead {
			start = (i + 1) % len(tenants)
			break
		}
	}
	s.lead = tenants[start]
	s.mu.Unlock()
	var out []tickItem
	for round := 0; round < perTenant; round++ {
		for i := range tenants {
			t := tenants[(start+i)%len(tenants)]
			if round < len(byTenant[t]) {
				out = append(out, byTenant[t][round])
			}
		}
	}
	return out
}

// each runs the items' steps one at a time in order (see order), each given at most deadline before the tick moves
// on.
func (s *tickScheduler) each(items []tickItem, perTenant int, deadline time.Duration) {
	slow := map[string]bool{}
	for _, it := range s.order(items, perTenant) {
		tenant := tickTenantOf(it.AccountId)
		if slow[tenant] {
			continue
		}
		s.mu.Lock()
		if s.inflight[it.Key] {
			s.mu.Unlock()
			continue
		}
		s.inflight[it.Key] = true
		s.mu.Unlock()
		done := make(chan struct{})
		run := it.Run
		key := it.Key
		go func() {
			defer close(done)
			defer func() {
				s.mu.Lock()
				delete(s.inflight, key)
				s.mu.Unlock()
			}()
			defer func() {
				if r := recover(); r != nil {
					slog.Error(s.name+": panic", "panic", fmt.Sprint(r), "stack", string(debug.Stack()), "row", key)
				}
			}()
			run()
		}()
		timer := time.NewTimer(deadline)
		select {
		case <-done:
			timer.Stop()
		case <-timer.C:
			slow[tenant] = true
			slog.Warn(s.name+": a step overran its time; the tenant's other rows wait for the next tick", "row", key,
				"account_id", it.AccountId, "deadline", deadline)
		}
	}
}
