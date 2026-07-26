package netem

import (
	"context"
	"math/rand"
	"net"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var tcpLossPauses = promauto.NewCounter(prometheus.CounterOpts{
	Name: "edgecast_netem_tcp_loss_pauses_total",
	Help: "Simulated TCP retransmission pauses injected by the link emulator",
})

// DialContext returns an impaired TCP connection for the HTTP path.
//
// TCP cannot be loss-impaired faithfully from userspace (loss happens below
// the congestion controller), so the emulator applies a model instead:
// per-read serialization at the rate cap, propagation delay on direction
// changes, and loss expressed as retransmission-like pauses (2x RTT per lost
// MSS-sized segment, iid). docs/03-experiment-design.md discusses validity.
func (st *State) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	// Handshake cost of the emulated link (one RTT).
	if rtt := st.Down().DelayMs + st.Up().DelayMs; rtt > 0 {
		time.Sleep(time.Duration(rtt * float64(time.Millisecond)))
	}
	return &tcpConn{Conn: conn, st: st, lastRead: time.Now()}, nil
}

type tcpConn struct {
	net.Conn
	st       *State
	mu       sync.Mutex
	nextFree time.Time
	lastRead time.Time
}

func (c *tcpConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.pace(n)
	}
	return n, err
}

func (c *tcpConn) Write(p []byte) (int, error) {
	if up := c.st.Up(); up.DelayMs > 0 {
		time.Sleep(time.Duration(up.DelayMs * float64(time.Millisecond)))
	}
	return c.Conn.Write(p)
}

func (c *tcpConn) pace(n int) {
	down := c.st.Down()
	if down.clean() {
		return
	}
	now := time.Now()
	c.mu.Lock()
	if c.nextFree.Before(now) {
		c.nextFree = now
	}
	var tx time.Duration
	// First bytes after an idle period pay propagation delay of the response.
	if now.Sub(c.lastRead) > 5*time.Millisecond && down.DelayMs > 0 {
		tx += time.Duration(down.DelayMs * float64(time.Millisecond))
	}
	c.lastRead = now
	if down.RateKbit > 0 {
		tx += time.Duration(float64(n) * 8 / down.RateKbit * float64(time.Millisecond))
	}
	if down.LossPct > 0 {
		rtt := down.DelayMs * 2
		if rtt < 10 {
			rtt = 10
		}
		segs := n/1448 + 1
		for i := 0; i < segs; i++ {
			if rand.Float64()*100 < down.LossPct {
				tx += time.Duration(2 * rtt * float64(time.Millisecond))
				tcpLossPauses.Inc()
			}
		}
	}
	c.nextFree = c.nextFree.Add(tx)
	due := c.nextFree
	c.mu.Unlock()
	if d := time.Until(due); d > 0 {
		time.Sleep(d)
	}
}
