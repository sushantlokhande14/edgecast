// Package relay implements the MoQ-style publish/subscribe relay.
//
// Architectural properties it reproduces from moq-transport:
//   - tracks -> groups -> objects, one QUIC uni stream per group
//   - fanout without terminating media sessions (objects forwarded as they
//     arrive, not store-and-forward per group)
//   - late join from a cached group so new subscribers start on a keyframe
//     immediately
//   - pull-through edges: a relay that lacks a track subscribes upstream
//   - per-subscriber queues that drop whole (oldest) groups under
//     backpressure, keeping everything delivered decodable
package relay

import (
	"bufio"
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/quic-go/quic-go"

	"github.com/sushantlokhande14/edgecast/internal/proto"
	"github.com/sushantlokhande14/edgecast/internal/quicutil"
)

var (
	sessionsGauge = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "edgecast_relay_sessions",
		Help: "Open sessions by role",
	}, []string{"role"})
	tracksGauge = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "edgecast_relay_tracks",
		Help: "Known tracks",
	})
	bytesIn = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecast_relay_bytes_in_total",
		Help: "Media bytes ingested from publishers or upstream relays",
	})
	fanoutBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecast_relay_fanout_bytes_total",
		Help: "Media bytes written to subscribers",
	})
	objectsForwarded = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecast_relay_objects_forwarded_total",
		Help: "Objects written to subscribers",
	})
	groupsDropped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecast_relay_groups_dropped_total",
		Help: "Whole groups dropped from subscriber queues under backpressure",
	})
	lateJoins = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecast_relay_late_joins_total",
		Help: "Subscribers served the cached group on join",
	})
	upstreamSubs = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "edgecast_relay_upstream_subscriptions",
		Help: "Active pull-through subscriptions to the upstream relay",
	})
)

// groupBuffer is one group being filled by ingest while any number of
// subscriber writers stream it out concurrently.
type groupBuffer struct {
	seq    uint64
	mu     sync.Mutex
	cond   *sync.Cond
	objs   [][]byte // encoded objects (header + payload)
	closed bool
}

func newGroupBuffer(seq uint64) *groupBuffer {
	g := &groupBuffer{seq: seq}
	g.cond = sync.NewCond(&g.mu)
	return g
}

func (g *groupBuffer) append(obj []byte) {
	g.mu.Lock()
	g.objs = append(g.objs, obj)
	g.mu.Unlock()
	g.cond.Broadcast()
}

func (g *groupBuffer) close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
	g.cond.Broadcast()
}

// next blocks until object i exists or the group is closed. ok=false means
// the group ended before object i.
func (g *groupBuffer) next(i int) (obj []byte, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for {
		if i < len(g.objs) {
			return g.objs[i], true
		}
		if g.closed {
			return nil, false
		}
		g.cond.Wait()
	}
}

type track struct {
	name    string
	mu      sync.Mutex
	subs    map[*subscriber]struct{}
	live    *groupBuffer // newest group; also the late-join cache
	sourced bool         // fed by a local publisher or an upstream subscription
}

func (t *track) addSub(s *subscriber) {
	t.mu.Lock()
	t.subs[s] = struct{}{}
	t.mu.Unlock()
}

func (t *track) removeSub(s *subscriber) {
	t.mu.Lock()
	delete(t.subs, s)
	t.mu.Unlock()
}

func (t *track) publishGroup(g *groupBuffer) {
	t.mu.Lock()
	t.live = g
	subs := make([]*subscriber, 0, len(t.subs))
	for s := range t.subs {
		subs = append(subs, s)
	}
	t.mu.Unlock()
	for _, s := range subs {
		s.enqueue(g)
	}
}

type subscriber struct {
	conn  quic.Connection
	track *track
	queue chan *groupBuffer
}

func newSubscriber(conn quic.Connection, t *track) *subscriber {
	return &subscriber{conn: conn, track: t, queue: make(chan *groupBuffer, 3)}
}

// enqueue never blocks ingest: when the queue is full the oldest queued
// group is dropped whole, which keeps everything delivered decodable.
func (s *subscriber) enqueue(g *groupBuffer) {
	for {
		select {
		case s.queue <- g:
			return
		default:
			select {
			case <-s.queue:
				groupsDropped.Inc()
			default:
			}
		}
	}
}

func (s *subscriber) run(ctx context.Context) {
	defer s.track.removeSub(s)
	var last uint64
	started := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.conn.Context().Done():
			return
		case g := <-s.queue:
			if started && g.seq <= last {
				continue
			}
			started = true
			last = g.seq
			if err := s.writeGroup(ctx, g); err != nil {
				return
			}
		}
	}
}

func (s *subscriber) writeGroup(ctx context.Context, g *groupBuffer) error {
	st, err := s.conn.OpenUniStreamSync(ctx)
	if err != nil {
		return err
	}
	if err := proto.WriteGroupHeader(st, s.track.name, g.seq); err != nil {
		return err
	}
	for i := 0; ; i++ {
		obj, ok := g.next(i)
		if !ok {
			break
		}
		if _, err := st.Write(obj); err != nil {
			return err
		}
		fanoutBytes.Add(float64(len(obj)))
		objectsForwarded.Inc()
	}
	return st.Close()
}

type Relay struct {
	region   string
	upstream string // parent relay address; empty for the origin
	mu       sync.Mutex
	tracks   map[string]*track
}

func New(region, upstream string) *Relay {
	return &Relay{region: region, upstream: upstream, tracks: map[string]*track{}}
}

func (r *Relay) track(name string) *track {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tracks[name]
	if !ok {
		t = &track{name: name, subs: map[*subscriber]struct{}{}}
		r.tracks[name] = t
		tracksGauge.Inc()
	}
	return t
}

func (r *Relay) ListenAndServe(ctx context.Context, addr string) error {
	tlsConf, err := quicutil.ServerTLS(proto.ALPN)
	if err != nil {
		return err
	}
	ln, err := quic.ListenAddr(addr, tlsConf, quicutil.Config())
	if err != nil {
		return err
	}
	log.Printf("relay %s listening on %s (upstream=%q)", r.region, addr, r.upstream)
	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			return err
		}
		go r.handleSession(ctx, conn)
	}
}

func (r *Relay) handleSession(ctx context.Context, conn quic.Connection) {
	defer conn.CloseWithError(0, "bye")
	ctrl, err := conn.AcceptStream(ctx)
	if err != nil {
		return
	}
	setup, err := proto.ReadControl(ctrl)
	if err != nil || setup.Type != proto.MsgSetup {
		return
	}
	role := setup.Role
	sessionsGauge.WithLabelValues(role).Inc()
	defer sessionsGauge.WithLabelValues(role).Dec()

	switch role {
	case proto.RolePublisher:
		r.handlePublisher(ctx, conn, ctrl)
	case proto.RoleSubscriber, proto.RoleRelay:
		r.handleSubscriber(ctx, conn, ctrl)
	default:
		_ = proto.WriteControl(ctrl, proto.Control{Type: proto.MsgError, Message: "unknown role"})
	}
}

func (r *Relay) handlePublisher(ctx context.Context, conn quic.Connection, ctrl quic.Stream) {
	msg, err := proto.ReadControl(ctrl)
	if err != nil || msg.Type != proto.MsgAnnounce || msg.Track == "" {
		return
	}
	t := r.track(msg.Track)
	t.mu.Lock()
	t.sourced = true
	t.mu.Unlock()
	if err := proto.WriteControl(ctrl, proto.Control{Type: proto.MsgAnnounceOK, Track: msg.Track}); err != nil {
		return
	}
	log.Printf("relay %s: publisher announced track %q", r.region, msg.Track)
	for {
		st, err := conn.AcceptUniStream(ctx)
		if err != nil {
			return
		}
		go r.ingestGroupStream(st)
	}
}

// ingestGroupStream reads one group from a publisher or upstream relay and
// republishes it locally, forwarding objects as they arrive.
func (r *Relay) ingestGroupStream(st quic.ReceiveStream) {
	br := bufio.NewReaderSize(st, 64<<10)
	name, seq, err := proto.ReadGroupHeader(br)
	if err != nil {
		return
	}
	t := r.track(name)
	g := newGroupBuffer(seq)
	t.publishGroup(g)
	defer g.close()
	for {
		h, payload, err := proto.ReadObject(br)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("relay %s: group %d of %q aborted: %v", r.region, seq, name, err)
			}
			return
		}
		h.GroupSeq = seq
		enc := proto.EncodeObject(h, payload)
		bytesIn.Add(float64(len(enc)))
		g.append(enc)
	}
}

func (r *Relay) handleSubscriber(ctx context.Context, conn quic.Connection, ctrl quic.Stream) {
	for {
		msg, err := proto.ReadControl(ctrl)
		if err != nil {
			return
		}
		if msg.Type != proto.MsgSubscribe || msg.Track == "" {
			continue
		}
		t := r.track(msg.Track)
		r.ensureSourced(ctx, t)

		sub := newSubscriber(conn, t)
		t.addSub(sub)
		t.mu.Lock()
		live := t.live
		var cur uint64
		if live != nil {
			cur = live.seq
		}
		t.mu.Unlock()
		if err := proto.WriteControl(ctrl, proto.Control{Type: proto.MsgSubscribeOK, Track: msg.Track, GroupSeq: cur}); err != nil {
			t.removeSub(sub)
			return
		}
		if live != nil {
			lateJoins.Inc()
			sub.enqueue(live)
		}
		go sub.run(ctx)
	}
}

// ensureSourced starts a pull-through subscription for tracks this relay does
// not yet carry, if it has an upstream to pull from.
func (r *Relay) ensureSourced(ctx context.Context, t *track) {
	t.mu.Lock()
	need := !t.sourced && r.upstream != ""
	if need {
		t.sourced = true
	}
	t.mu.Unlock()
	if need {
		go r.upstreamLoop(ctx, t)
	}
}

func (r *Relay) upstreamLoop(ctx context.Context, t *track) {
	for ctx.Err() == nil {
		err := r.pullUpstream(ctx, t)
		if ctx.Err() != nil {
			return
		}
		log.Printf("relay %s: upstream subscription for %q ended: %v; retrying", r.region, t.name, err)
		time.Sleep(2 * time.Second)
	}
}

func (r *Relay) pullUpstream(ctx context.Context, t *track) error {
	conn, err := quicutil.Dial(ctx, r.upstream, proto.ALPN)
	if err != nil {
		return err
	}
	defer conn.CloseWithError(0, "upstream done")
	upstreamSubs.Inc()
	defer upstreamSubs.Dec()

	ctrl, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	if err := proto.WriteControl(ctrl, proto.Control{Type: proto.MsgSetup, Role: proto.RoleRelay}); err != nil {
		return err
	}
	if err := proto.WriteControl(ctrl, proto.Control{Type: proto.MsgSubscribe, Track: t.name}); err != nil {
		return err
	}
	if _, err := proto.ReadControl(ctrl); err != nil {
		return err
	}
	log.Printf("relay %s: pulling track %q from upstream %s", r.region, t.name, r.upstream)
	for {
		st, err := conn.AcceptUniStream(ctx)
		if err != nil {
			return err
		}
		go r.ingestGroupStream(st)
	}
}
