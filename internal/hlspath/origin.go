// Package hlspath is the HTTP adaptive streaming reference path: a live
// sliding-window origin with a 3-rendition ladder and an ABR player
// simulation measured by the shared loadgen recorder.
package hlspath

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/sushantlokhande14/edgecast/internal/admin"
)

var (
	segmentsServed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "edgecast_hls_segments_served_total", Help: "Segments served by rendition",
	}, []string{"rendition"})
	originBytes = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecast_hls_origin_bytes_total", Help: "Segment bytes served",
	})
)

const (
	SegmentSeconds = 2
	windowSegments = 6
	ringSegments   = 12
	createdHeader  = "X-Edgecast-Created-Micros"
)

// Rendition ladder. Bandwidth values feed the master playlist and segment
// sizing (kbps * 1000/8 * SegmentSeconds bytes per segment).
var Renditions = []struct {
	Name string
	Kbps int
}{
	{"v0", 600},
	{"v1", 1500},
	{"v2", 3000},
}

type segment struct {
	seq           int
	createdMicros int64
	data          []byte
}

type Origin struct {
	mu   sync.Mutex
	segs map[string][]segment // rendition -> ring, oldest first
	seq  int
	rnd  []byte
}

func NewOrigin() *Origin {
	o := &Origin{segs: map[string][]segment{}}
	o.rnd = make([]byte, 8<<20)
	rand.New(rand.NewSource(7)).Read(o.rnd)
	o.produce() // segment 0 exists immediately
	return o
}

// Run produces one segment per rendition every SegmentSeconds.
func (o *Origin) Run(ctx context.Context) {
	t := time.NewTicker(SegmentSeconds * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			o.produce()
		}
	}
}

func (o *Origin) produce() {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now().UnixMicro()
	for _, r := range Renditions {
		size := r.Kbps * 1000 / 8 * SegmentSeconds
		off := (o.seq * 40961) % (len(o.rnd) - size)
		ring := append(o.segs[r.Name], segment{seq: o.seq, createdMicros: now, data: o.rnd[off : off+size]})
		if len(ring) > ringSegments {
			ring = ring[len(ring)-ringSegments:]
		}
		o.segs[r.Name] = ring
	}
	o.seq++
}

func (o *Origin) RegisterHandlers(a *admin.Server) {
	a.Handle("GET /hls/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		var b strings.Builder
		b.WriteString("#EXTM3U\n")
		for _, rd := range Renditions {
			fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d\n%s/live.m3u8\n", rd.Kbps*1000, rd.Name)
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte(b.String()))
	})
	a.Handle("GET /hls/{rendition}/live.m3u8", func(w http.ResponseWriter, r *http.Request) {
		rend := r.PathValue("rendition")
		o.mu.Lock()
		ring := append([]segment(nil), o.segs[rend]...)
		o.mu.Unlock()
		if ring == nil {
			http.NotFound(w, r)
			return
		}
		if len(ring) > windowSegments {
			ring = ring[len(ring)-windowSegments:]
		}
		var b strings.Builder
		b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
		fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", SegmentSeconds)
		if len(ring) > 0 {
			fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n", ring[0].seq)
		}
		for _, s := range ring {
			fmt.Fprintf(&b, "#EXTINF:%d.0,\nseg%d.ts\n", SegmentSeconds, s.seq)
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte(b.String()))
	})
	a.Handle("GET /hls/{rendition}/{seg}", func(w http.ResponseWriter, r *http.Request) {
		rend := r.PathValue("rendition")
		name := r.PathValue("seg")
		if !strings.HasPrefix(name, "seg") || !strings.HasSuffix(name, ".ts") {
			http.NotFound(w, r)
			return
		}
		seq, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "seg"), ".ts"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		o.mu.Lock()
		var found *segment
		for i := range o.segs[rend] {
			if o.segs[rend][i].seq == seq {
				found = &o.segs[rend][i]
				break
			}
		}
		o.mu.Unlock()
		if found == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set(createdHeader, strconv.FormatInt(found.createdMicros, 10))
		_, _ = w.Write(found.data)
		segmentsServed.WithLabelValues(rend).Inc()
		originBytes.Add(float64(len(found.data)))
	})
}
