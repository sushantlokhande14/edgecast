// Package moqclient implements the MoQ-path publisher and subscriber roles.
package moqclient

import (
	"context"
	"log"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/quic-go/quic-go"

	"github.com/sushantlokhande14/edgecast/internal/media"
	"github.com/sushantlokhande14/edgecast/internal/proto"
	"github.com/sushantlokhande14/edgecast/internal/quicutil"
)

var (
	pubBitrate = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "edgecast_pub_bitrate_kbps",
		Help: "Current publisher bitrate tier",
	})
	pubTierChanges = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecast_pub_tier_changes_total",
		Help: "ABR ladder moves",
	})
	pubBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecast_pub_bytes_sent_total",
		Help: "Media bytes written to the relay",
	})
	pubGroupWrite = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "edgecast_pub_group_write_seconds",
		Help:    "Total time Write blocked per group; approaches the group duration when congestion control is starved",
		Buckets: []float64{0.001, 0.005, 0.02, 0.05, 0.1, 0.2, 0.35, 0.5, 0.75, 1, 1.5, 2},
	})
)

// Ladder is the shared bitrate ladder (kbps) for all protocol paths.
var Ladder = []int{250, 500, 1000, 2500, 5000}

type PubConfig struct {
	RelayAddr   string
	Track       string
	StartKbps   int
	ABR         bool
	Media       media.Config
}

// RunPublisher publishes the synthetic track forever, reconnecting on failure.
func RunPublisher(ctx context.Context, cfg PubConfig) {
	tier := 0
	for i, k := range Ladder {
		if k == cfg.StartKbps {
			tier = i
		}
	}
	gen := media.NewGenerator(cfg.Media, Ladder[tier])
	pubBitrate.Set(float64(Ladder[tier]))
	for ctx.Err() == nil {
		err := publishOnce(ctx, cfg, gen, &tier)
		if ctx.Err() != nil {
			return
		}
		log.Printf("moq-pub: session ended: %v; reconnecting", err)
		time.Sleep(2 * time.Second)
	}
}

func publishOnce(ctx context.Context, cfg PubConfig, gen *media.Generator, tier *int) error {
	conn, err := quicutil.Dial(ctx, cfg.RelayAddr, proto.ALPN)
	if err != nil {
		return err
	}
	defer conn.CloseWithError(0, "pub done")

	ctrl, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	if err := proto.WriteControl(ctrl, proto.Control{Type: proto.MsgSetup, Role: proto.RolePublisher}); err != nil {
		return err
	}
	if err := proto.WriteControl(ctrl, proto.Control{Type: proto.MsgAnnounce, Track: cfg.Track}); err != nil {
		return err
	}
	if _, err := proto.ReadControl(ctrl); err != nil {
		return err
	}
	log.Printf("moq-pub: announced %q to %s at %d kbps (abr=%v)", cfg.Track, cfg.RelayAddr, gen.BitrateKbps(), cfg.ABR)

	ticker := time.NewTicker(gen.FrameInterval())
	defer ticker.Stop()

	var st quic.SendStream
	var writeDur time.Duration
	groupDur := gen.GroupDuration()
	slow, fast := 0, 0

	for {
		select {
		case <-ctx.Done():
			if st != nil {
				st.Close()
			}
			return nil
		case <-ticker.C:
		}
		f := gen.Next(time.Now())
		if f.ObjectSeq == 0 {
			if st != nil {
				st.Close()
				pubGroupWrite.Observe(writeDur.Seconds())
				if cfg.ABR {
					adapt(gen, tier, writeDur, groupDur, &slow, &fast)
				}
				writeDur = 0
			}
			st, err = conn.OpenUniStreamSync(ctx)
			if err != nil {
				return err
			}
			if err := proto.WriteGroupHeader(st, cfg.Track, f.GroupSeq); err != nil {
				return err
			}
		}
		if st == nil {
			continue // first frames until a group boundary aligns
		}
		enc := proto.EncodeObject(proto.ObjectHeader{
			GroupSeq: f.GroupSeq, ObjectSeq: f.ObjectSeq,
			PTSMicros: f.PTSMicros, Keyframe: f.Keyframe,
		}, f.Payload)
		w0 := time.Now()
		if _, err := st.Write(enc); err != nil {
			return err
		}
		writeDur += time.Since(w0)
		pubBytes.Add(float64(len(enc)))
	}
}

// adapt implements backpressure-driven ABR: when QUIC congestion control is
// starved, stream writes block and per-group write time approaches the group
// duration. Sustained slowness steps the ladder down; sustained headroom
// steps it up, with hysteresis.
func adapt(gen *media.Generator, tier *int, writeDur, groupDur time.Duration, slow, fast *int) {
	ratio := writeDur.Seconds() / groupDur.Seconds()
	switch {
	case ratio > 0.5:
		*slow++
		*fast = 0
	case ratio < 0.1:
		*fast++
		*slow = 0
	default:
		*slow, *fast = 0, 0
	}
	if *slow >= 2 && *tier > 0 {
		*tier--
		*slow = 0
		gen.SetBitrateKbps(Ladder[*tier])
		pubBitrate.Set(float64(Ladder[*tier]))
		pubTierChanges.Inc()
		log.Printf("moq-pub: ABR down to %d kbps (group write ratio %.2f)", Ladder[*tier], ratio)
	}
	if *fast >= 10 && *tier < len(Ladder)-1 {
		*tier++
		*fast = 0
		gen.SetBitrateKbps(Ladder[*tier])
		pubBitrate.Set(float64(Ladder[*tier]))
		pubTierChanges.Inc()
		log.Printf("moq-pub: ABR up to %d kbps", Ladder[*tier])
	}
}
