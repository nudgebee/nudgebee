package handlers

import "sync"

// replyRouter hands each agent reply to the delivery waiting on its request ID.
//
// The WebSocket reader is the session's only consumer of the agent's socket,
// so routing must never block it: a reader stuck on one reply stops reading
// everything, the agent's writes back up until they time out, and — since the
// session's pinger keeps running — neither side ever ends the session.
//
// The same request ID can be in flight more than once (the same message
// delivered to the agent again while the first copy is still waiting), so an
// ID maps to a list of waiters rather than one. Each waiter takes exactly one
// reply and leaves the list when it gets it — it may still be busy with that
// reply, and a later reply must go to the next waiter, not back to it.
type replyRouter struct {
	mu sync.Mutex
	m  map[string][]chan []byte
}

func newReplyRouter() *replyRouter {
	return &replyRouter{m: make(map[string][]chan []byte)}
}

// register adds a waiter for id. The returned release removes that waiter —
// and only that one, so a copy finishing early cannot unregister another copy
// still waiting on the same ID.
func (r *replyRouter) register(id string) (<-chan []byte, func()) {
	ch := make(chan []byte, 1)
	r.mu.Lock()
	r.m[id] = append(r.m[id], ch)
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		for i, w := range r.m[id] {
			if w == ch {
				r.removeAt(id, i)
				return
			}
		}
	}
}

// removeAt drops the i-th waiter on id. It copies rather than shifting in
// place, so a slice route is iterating is never mutated under it. Callers hold
// r.mu.
func (r *replyRouter) removeAt(id string, i int) {
	waiters := r.m[id]
	if len(waiters) <= 1 {
		delete(r.m, id)
		return
	}
	rest := make([]chan []byte, 0, len(waiters)-1)
	rest = append(rest, waiters[:i]...)
	r.m[id] = append(rest, waiters[i+1:]...)
}

// route hands raw to the first waiter on id and removes that waiter. It never
// blocks. found reports whether id had any waiter; delivered whether one took
// the reply. Every waiter's slot is empty while it is listed, so delivered is
// false only if the id has no waiter left — a duplicate or late reply.
func (r *replyRouter) route(id string, raw []byte) (found, delivered bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	waiters := r.m[id]
	for i, ch := range waiters {
		select {
		case ch <- raw:
			r.removeAt(id, i)
			return true, true
		default:
		}
	}
	return len(waiters) > 0, false
}
