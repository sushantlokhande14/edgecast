// Package media produces synthetic media frames: realistic sizes and cadence,
// incompressible payloads, no codec. Keeping the source synthetic isolates
// transport behavior from encoder behavior and lets one generator drive all
// three protocol paths identically.
package media

import (
	"math/rand"
	"sync/atomic"
	"time"
)

// Frame is one synthetic frame in decode order.
type Frame struct {
	GroupSeq  uint64 // group (GOP) sequence, starts at 0
	ObjectSeq uint64 // frame index within the group; 0 is the keyframe
	Keyframe  bool
	PTSMicros uint64 // publisher wall clock, microseconds since epoch
	Payload   []byte
}

type Config struct {
	FPS         int     // frames per second
	GroupFrames int     // frames per group; a keyframe starts each group
	KeyframeMul float64 // keyframe size relative to the average frame size
}

func DefaultConfig() Config {
	return Config{FPS: 30, GroupFrames: 30, KeyframeMul: 4.0}
}

// Generator emits frames whose sizes average out to the configured bitrate.
// Bitrate can be changed at any time (ABR); the change takes effect on the
// next frame. Not safe for concurrent Next calls; SetBitrateKbps is safe.
type Generator struct {
	cfg     Config
	bitrate atomic.Int64 // kbps
	group   uint64
	object  uint64
	rnd     []byte
}

func NewGenerator(cfg Config, bitrateKbps int) *Generator {
	if cfg.FPS <= 0 {
		cfg.FPS = 30
	}
	if cfg.GroupFrames <= 1 {
		cfg.GroupFrames = 30
	}
	if cfg.KeyframeMul <= 1 {
		cfg.KeyframeMul = 4.0
	}
	g := &Generator{cfg: cfg}
	g.bitrate.Store(int64(bitrateKbps))
	g.rnd = make([]byte, 4<<20)
	rand.New(rand.NewSource(42)).Read(g.rnd)
	return g
}

func (g *Generator) SetBitrateKbps(k int) { g.bitrate.Store(int64(k)) }
func (g *Generator) BitrateKbps() int     { return int(g.bitrate.Load()) }
func (g *Generator) FrameInterval() time.Duration {
	return time.Second / time.Duration(g.cfg.FPS)
}
func (g *Generator) GroupDuration() time.Duration {
	return g.FrameInterval() * time.Duration(g.cfg.GroupFrames)
}

// Next returns the next frame in decode order and advances the sequence.
// Payload slices alias a shared random buffer; treat them as read-only.
func (g *Generator) Next(now time.Time) Frame {
	kf := g.object == 0
	avg := float64(g.BitrateKbps()) * 1000 / 8 / float64(g.cfg.FPS)
	n := float64(g.cfg.GroupFrames)
	kfBytes := avg * g.cfg.KeyframeMul
	deltaBytes := (avg*n - kfBytes) / (n - 1)
	if deltaBytes < 64 {
		deltaBytes = 64
	}
	size := int(deltaBytes)
	if kf {
		size = int(kfBytes)
	}
	if size < 64 {
		size = 64
	}
	if size > len(g.rnd)/2 {
		size = len(g.rnd) / 2
	}
	off := int((g.group*7919 + g.object*131) % uint64(len(g.rnd)-size))
	f := Frame{
		GroupSeq:  g.group,
		ObjectSeq: g.object,
		Keyframe:  kf,
		PTSMicros: uint64(now.UnixMicro()),
		Payload:   g.rnd[off : off+size],
	}
	g.object++
	if int(g.object) >= g.cfg.GroupFrames {
		g.group++
		g.object = 0
	}
	return f
}
