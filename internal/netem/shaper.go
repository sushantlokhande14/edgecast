// Package netem is a userspace WAN emulator. The testbed originally planned
// kernel tc/netem, but Docker Desktop's WSL2 kernel does not ship sch_netem,
// so impairment is applied in-process instead: an impairing net.PacketConn
// that QUIC and WebRTC's ICE mux dial through, and a TCP conn wrapper for the
// HTTP path. Upside of the pivot: the testbed now runs identically on any
// Docker host, no kernel modules or NET_ADMIN required.
package netem

import (
	"container/heap"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var droppedPackets = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "edgecast_netem_dropped_packets_total",
	Help: "Packets dropped by the link emulator",
}, []string{"dir", "cause"})

// Profile is one direction of an emulated link. Zero value = clean.
type Profile struct {
	DelayMs  float64 // one-way propagation delay
	JitterMs float64 // uniform +/- around DelayMs
	LossPct  float64 // iid packet loss, percent
	RateKbit float64 // serialization rate cap; 0 = unlimited
}

func (p Profile) clean() bool {
	return p.DelayMs == 0 && p.JitterMs == 0 && p.LossPct == 0 && p.RateKbit == 0
}

// maxQueueDelay caps the emulated bottleneck queue (bufferbloat guard):
// packets that would wait longer than this for serialization are tail-dropped.
const maxQueueDelay = 200 * time.Millisecond

type packet struct {
	due  time.Time
	seq  uint64
	buf  []byte
	addr net.Addr
}

type pktHeap []*packet

func (h pktHeap) Len() int { return len(h) }
func (h pktHeap) Less(i, j int) bool {
	if h[i].due.Equal(h[j].due) {
		return h[i].seq < h[j].seq
	}
	return h[i].due.Before(h[j].due)
}
func (h pktHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *pktHeap) Push(x any)   { *h = append(*h, x.(*packet)) }
func (h *pktHeap) Pop() any {
	old := *h
	n := len(old)
	p := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return p
}

// shaper models one link direction: token-bucket-style serialization at
// RateKbit with a bounded queue, then propagation delay with jitter. Jittered
// packets may reorder, as with kernel netem. Delivery order and timing are
// driven by a single scheduler goroutine.
type shaper struct {
	profile func() Profile
	deliver func(buf []byte, addr net.Addr)
	dir     string

	mu       sync.Mutex
	h        pktHeap
	nextFree time.Time
	seq      uint64
	wake     chan struct{}
	done     chan struct{}
	once     sync.Once
}

func newShaper(dir string, profile func() Profile, deliver func([]byte, net.Addr)) *shaper {
	s := &shaper{
		profile: profile,
		deliver: deliver,
		dir:     dir,
		wake:    make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	go s.loop()
	return s
}

func (s *shaper) close() { s.once.Do(func() { close(s.done) }) }

// send accepts a packet for transmission. The caller's buffer is only
// retained if the packet is queued (it is copied first), so callers may reuse
// it. Delivery can happen synchronously on the clean-link fast path.
func (s *shaper) send(buf []byte, addr net.Addr) {
	p := s.profile()
	if p.LossPct > 0 && rand.Float64()*100 < p.LossPct {
		droppedPackets.WithLabelValues(s.dir, "loss").Inc()
		return
	}
	now := time.Now()

	s.mu.Lock()
	slot := now
	if p.RateKbit > 0 {
		if s.nextFree.Before(now) {
			s.nextFree = now
		}
		if s.nextFree.Sub(now) > maxQueueDelay {
			s.mu.Unlock()
			droppedPackets.WithLabelValues(s.dir, "queue").Inc()
			return
		}
		// bits / kbit-per-s == milliseconds
		tx := time.Duration(float64(len(buf)) * 8 / p.RateKbit * float64(time.Millisecond))
		slot = s.nextFree
		s.nextFree = slot.Add(tx)
	}
	owd := p.DelayMs
	if p.JitterMs > 0 {
		owd += (rand.Float64()*2 - 1) * p.JitterMs
	}
	if owd < 0 {
		owd = 0
	}
	due := slot.Add(time.Duration(owd * float64(time.Millisecond)))
	if !due.After(now) && len(s.h) == 0 {
		s.mu.Unlock()
		s.deliver(buf, addr)
		return
	}
	cp := make([]byte, len(buf))
	copy(cp, buf)
	s.seq++
	heap.Push(&s.h, &packet{due: due, seq: s.seq, buf: cp, addr: addr})
	s.mu.Unlock()

	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *shaper) loop() {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	var ready []*packet
	for {
		now := time.Now()
		wait := time.Hour
		ready = ready[:0]
		s.mu.Lock()
		for len(s.h) > 0 {
			top := s.h[0]
			if top.due.After(now) {
				wait = top.due.Sub(now)
				break
			}
			ready = append(ready, heap.Pop(&s.h).(*packet))
		}
		s.mu.Unlock()
		for _, p := range ready {
			s.deliver(p.buf, p.addr)
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(wait)
		select {
		case <-s.done:
			return
		case <-s.wake:
		case <-timer.C:
		}
	}
}
