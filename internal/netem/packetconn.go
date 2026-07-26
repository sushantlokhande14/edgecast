package netem

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

type recvPkt struct {
	buf  []byte
	addr net.Addr
}

// PacketConn wraps a UDP socket with the emulated link: egress packets pass
// through the uplink shaper, ingress through the downlink shaper. It
// satisfies net.PacketConn, so quic-go and Pion's ICE UDP mux use it as-is.
type PacketConn struct {
	inner net.PacketConn
	up    *shaper
	down  *shaper

	readCh chan recvPkt
	closed chan struct{}
	once   sync.Once

	dlMu     sync.Mutex
	deadline time.Time
	dlNotify chan struct{}
}

func NewPacketConn(inner net.PacketConn, st *State) *PacketConn {
	c := &PacketConn{
		inner:    inner,
		readCh:   make(chan recvPkt, 1024),
		closed:   make(chan struct{}),
		dlNotify: make(chan struct{}),
	}
	c.up = newShaper("up", st.Up, func(b []byte, addr net.Addr) {
		_, _ = inner.WriteTo(b, addr)
	})
	c.down = newShaper("down", st.Down, c.deliverDown)
	go c.pump()
	return c
}

func (c *PacketConn) pump() {
	buf := make([]byte, 65535)
	for {
		n, addr, err := c.inner.ReadFrom(buf)
		if err != nil {
			c.Close()
			return
		}
		c.down.send(buf[:n], addr)
	}
}

func (c *PacketConn) deliverDown(b []byte, addr net.Addr) {
	cp := make([]byte, len(b))
	copy(cp, b)
	select {
	case c.readCh <- recvPkt{cp, addr}:
	default:
		droppedPackets.WithLabelValues("down", "appqueue").Inc()
	}
}

func (c *PacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		c.dlMu.Lock()
		dl := c.deadline
		notify := c.dlNotify
		c.dlMu.Unlock()

		var timeout <-chan time.Time
		var timer *time.Timer
		if !dl.IsZero() {
			d := time.Until(dl)
			if d <= 0 {
				return 0, nil, os.ErrDeadlineExceeded
			}
			timer = time.NewTimer(d)
			timeout = timer.C
		}
		select {
		case pkt := <-c.readCh:
			if timer != nil {
				timer.Stop()
			}
			n := copy(p, pkt.buf)
			return n, pkt.addr, nil
		case <-c.closed:
			if timer != nil {
				timer.Stop()
			}
			return 0, nil, net.ErrClosed
		case <-timeout:
			return 0, nil, os.ErrDeadlineExceeded
		case <-notify:
			if timer != nil {
				timer.Stop()
			}
		}
	}
}

func (c *PacketConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	c.up.send(p, addr)
	return len(p), nil
}

func (c *PacketConn) Close() error {
	c.once.Do(func() {
		close(c.closed)
		c.up.close()
		c.down.close()
		_ = c.inner.Close()
	})
	return nil
}

func (c *PacketConn) LocalAddr() net.Addr { return c.inner.LocalAddr() }

func (c *PacketConn) SetReadDeadline(t time.Time) error {
	c.dlMu.Lock()
	c.deadline = t
	close(c.dlNotify)
	c.dlNotify = make(chan struct{})
	c.dlMu.Unlock()
	return nil
}

func (c *PacketConn) SetDeadline(t time.Time) error      { return c.SetReadDeadline(t) }
func (c *PacketConn) SetWriteDeadline(t time.Time) error { return nil }

// DialQUIC dials a QUIC connection through the emulated link. The returned
// PacketConn must be closed by the caller after the connection ends.
func DialQUIC(ctx context.Context, st *State, addr string, tlsConf *tls.Config, qconf *quic.Config) (quic.Connection, *PacketConn, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, nil, err
	}
	sock, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, nil, err
	}
	pc := NewPacketConn(sock, st)
	dctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := quic.Dial(dctx, pc, udpAddr, tlsConf, qconf)
	if err != nil {
		pc.Close()
		return nil, nil, err
	}
	return conn, pc, nil
}
