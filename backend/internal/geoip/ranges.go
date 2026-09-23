package geoip

import (
	"math"
	"net/netip"
	"slices"
)

// The range indexes — RIPE's registrations, the operators' geofeeds — are
// built the same way: address ranges that nest, each carrying a value, flattened
// into sorted starts that a binary search answers.

// rangeAddr is an address the indexes can order and step through: 32-bit for
// IPv4 and 128-bit for IPv6, kept apart so four million IPv4 ranges cost four
// bytes an address rather than sixteen.
type rangeAddr[A any] interface {
	comparable
	less(A) bool
	next() (A, bool) // false past the last address
	prev() (A, bool) // false before the first
}

type v4addr uint32

func (a v4addr) less(b v4addr) bool { return a < b }

func (a v4addr) next() (v4addr, bool) {
	if a == math.MaxUint32 {
		return 0, false
	}
	return a + 1, true
}

func (a v4addr) prev() (v4addr, bool) {
	if a == 0 {
		return 0, false
	}
	return a - 1, true
}

type v6addr struct{ hi, lo uint64 }

func (a v6addr) less(b v6addr) bool { return a.hi < b.hi || a.hi == b.hi && a.lo < b.lo }

func (a v6addr) next() (v6addr, bool) {
	if a.lo != math.MaxUint64 {
		return v6addr{a.hi, a.lo + 1}, true
	}
	if a.hi != math.MaxUint64 {
		return v6addr{a.hi + 1, 0}, true
	}
	return v6addr{}, false
}

func (a v6addr) prev() (v6addr, bool) {
	if a.lo != 0 {
		return v6addr{a.hi, a.lo - 1}, true
	}
	if a.hi != 0 {
		return v6addr{a.hi - 1, math.MaxUint64}, true
	}
	return v6addr{}, false
}

// lastIn is the last address of the prefix of the given length starting at a.
func (a v6addr) lastIn(bits int) v6addr {
	switch host := 128 - bits; {
	case host >= 128:
		return v6addr{math.MaxUint64, math.MaxUint64}
	case host >= 64:
		return v6addr{a.hi | (1<<(host-64) - 1), math.MaxUint64}
	default:
		return v6addr{a.hi, a.lo | (1<<host - 1)}
	}
}

func v4of(a netip.Addr) v4addr {
	b := a.As4()
	return v4addr(be.Uint32(b[:]))
}

func v6of(a netip.Addr) v6addr {
	b := a.As16()
	return v6addr{be.Uint64(b[:8]), be.Uint64(b[8:])}
}

func (a v4addr) addr() netip.Addr {
	return netip.AddrFrom4([4]byte{byte(a >> 24), byte(a >> 16), byte(a >> 8), byte(a)})
}

func (a v6addr) addr() netip.Addr {
	var b [16]byte
	be.PutUint64(b[:8], a.hi)
	be.PutUint64(b[8:], a.lo)
	return netip.AddrFrom16(b)
}

// span is a range of addresses, both ends included, carrying a value: the
// country a block is registered to, the place a geofeed entry names.
type span[A rangeAddr[A], V comparable] struct {
	start, end A
	val        V
}

// flatten turns nested spans into a partition of the address space: sorted
// starts, each beginning a stretch that takes its value from the innermost
// span over it, and V's zero value where there is none. Adjacent stretches
// with the same value are merged, which is most of the saving: an allocation
// holding a thousand same-country assignments becomes one stretch.
//
// Spans are ordered by start, outermost first, and swept with a stack of the
// ones still open. Where two merely overlap, the later one is treated as the
// more specific over the overlap, as a later, narrower registration would
// imply. Two spans over the same range are settled by order, the same way
// every time.
func flatten[A rangeAddr[A], V comparable](spans []span[A, V], order func(V, V) int) (starts []A, vals []V) {
	slices.SortFunc(spans, func(x, y span[A, V]) int {
		switch {
		case x.start.less(y.start):
			return -1
		case y.start.less(x.start):
			return 1
		case y.end.less(x.end): // same start: the wider span is the outer one
			return -1
		case x.end.less(y.end):
			return 1
		}
		return order(x.val, y.val)
	})

	var none V
	emit := func(at A, v V) {
		n := len(starts)
		if n > 0 && starts[n-1] == at {
			// Nothing lies between: the later, more specific claim replaces it.
			vals[n-1] = v
			if n > 1 && vals[n-2] == v {
				starts, vals = starts[:n-1], vals[:n-1]
			}
			return
		}
		if n > 0 && vals[n-1] == v {
			return // continues the stretch before
		}
		if n == 0 && v == none {
			return // nothing to record before the first span
		}
		starts = append(starts, at)
		vals = append(vals, v)
	}

	var open []span[A, V]
	// closeUntil ends every open span that ends before limit, or every one.
	closeUntil := func(limit A, all bool) {
		for len(open) > 0 && (all || open[len(open)-1].end.less(limit)) {
			top := open[len(open)-1]
			open = open[:len(open)-1]
			at, more := top.end.next()
			if !more {
				open = open[:0] // the end of the address space
				return
			}
			// Spans beneath that ended no later than this one are over too.
			for len(open) > 0 && open[len(open)-1].end.less(at) {
				open = open[:len(open)-1]
			}
			if len(open) > 0 {
				emit(at, open[len(open)-1].val)
			} else {
				emit(at, none)
			}
		}
	}

	for _, s := range spans {
		closeUntil(s.start, false)
		emit(s.start, s.val)
		open = append(open, s)
	}
	var zero A
	closeUntil(zero, true)
	return starts, vals
}
