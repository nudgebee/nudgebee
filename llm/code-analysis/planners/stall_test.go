package planners

import "testing"

// TestLedgerMadeProgress pins the deterministic stall signal: a reflection
// counts as progress iff the ledger grew (finding/citation), a sub-question
// closed, or the first edit appeared. Everything else is "no progress", which
// is what the stall detector accumulates against maxStallReflections.
func TestLedgerMadeProgress(t *testing.T) {
	cases := []struct {
		name       string
		pf, pc, pq int
		pe         bool
		f, c, q    int
		e          bool
		want       bool
	}{
		{name: "new finding", pf: 1, f: 2, want: true},
		{name: "new citation", pc: 1, c: 2, want: true},
		{name: "closed sub-question", pq: 3, q: 2, want: true},
		{name: "first edit appears", pe: false, e: true, want: true},
		{name: "no change at all", pf: 2, pc: 1, pq: 2, f: 2, c: 1, q: 2, want: false},
		{name: "open questions grew (not progress)", pq: 2, q: 4, want: false},
		{name: "already editing, ledger static (not progress)", pe: true, pf: 2, e: true, f: 2, want: false},
		{name: "finding count dropped after cull (not progress)", pf: 5, f: 3, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ledgerMadeProgress(tc.pf, tc.pc, tc.pq, tc.pe, tc.f, tc.c, tc.q, tc.e)
			if got != tc.want {
				t.Errorf("ledgerMadeProgress(prev{f:%d,c:%d,q:%d,e:%v}, cur{f:%d,c:%d,q:%d,e:%v}) = %v, want %v",
					tc.pf, tc.pc, tc.pq, tc.pe, tc.f, tc.c, tc.q, tc.e, got, tc.want)
			}
		})
	}
}
