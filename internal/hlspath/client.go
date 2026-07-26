package hlspath

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/sushantlokhande14/edgecast/internal/loadgen"
	"github.com/sushantlokhande14/edgecast/internal/netem"
)

var segmentFetches = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "edgecast_hls_segment_fetches_total", Help: "Client segment downloads by rendition",
}, []string{"rendition"})

const (
	startupBufferMs = 2 * SegmentSeconds * 1000 // play after 2 segments
	targetBufferMs  = 10_000
)

// ConfigureRecorder switches a Recorder to the HLS accounting mode: the
// player's own buffer model owns startup and stall decisions, because
// segment arrivals are inherently bursty.
func ConfigureRecorder(rec *loadgen.Recorder) {
	rec.ManualStartup = true
	rec.AutoStall = false
}

// RunSession simulates one ABR player against the origin through the
// emulated link: EWMA bandwidth estimation picks the rendition, a buffer
// targeting 10s drains in real time, underruns count as stalls.
func RunSession(ctx context.Context, link *netem.State, originAddr string, rec *loadgen.Recorder) error {
	client := &http.Client{
		Timeout:   60 * time.Second,
		Transport: &http.Transport{DialContext: link.DialContext},
	}
	base := "http://" + originAddr

	renditions, err := fetchMaster(ctx, client, base)
	if err != nil {
		return err
	}

	cur := 0 // start conservative on the lowest rendition
	var bwKbps float64
	var bufferMs float64
	started := false
	lastTick := time.Now()
	nextSeq := -1

	drain := func(now time.Time) {
		if started {
			bufferMs -= float64(now.Sub(lastTick).Milliseconds())
			if bufferMs < 0 {
				rec.AddStall(now, -bufferMs)
				bufferMs = 0
			}
		}
		lastTick = now
	}

	for ctx.Err() == nil {
		pl, err := fetchPlaylist(ctx, client, base, renditions[cur].uri)
		if err != nil {
			return err
		}
		if nextSeq < 0 {
			// join 2 segments behind the live edge
			nextSeq = pl.firstSeq + len(pl.segments) - 2
			if nextSeq < pl.firstSeq {
				nextSeq = pl.firstSeq
			}
		}
		fetched := false
		for _, s := range pl.segments {
			if s.seq != nextSeq {
				continue
			}
			t0 := time.Now()
			n, created, err := fetchSegment(ctx, client, base, renditions[cur].uri, s.name)
			if err != nil {
				return err
			}
			now := time.Now()
			drain(now)
			dl := now.Sub(t0).Seconds()
			if dl > 0 {
				sample := float64(n) * 8 / 1000 / dl
				if bwKbps == 0 {
					bwKbps = sample
				} else {
					bwKbps = 0.7*bwKbps + 0.3*sample
				}
			}
			rec.Media(now, uint64(created), n)
			segmentFetches.WithLabelValues(renditions[cur].uri).Inc()
			bufferMs += SegmentSeconds * 1000
			if !started && bufferMs >= startupBufferMs {
				started = true
				rec.MarkStartup(now)
				lastTick = now
			}
			nextSeq++
			fetched = true
			// ABR: highest rendition safely below the bandwidth estimate
			next := 0
			for i, r := range renditions {
				if float64(r.kbps) < 0.8*bwKbps {
					next = i
				}
			}
			cur = next
		}
		now := time.Now()
		drain(now)
		switch {
		case bufferMs > targetBufferMs:
			sleepCtx(ctx, time.Duration(bufferMs-targetBufferMs)*time.Millisecond/2)
		case !fetched:
			// live edge: wait for the next segment to be produced
			sleepCtx(ctx, 300*time.Millisecond)
		}
	}
	return nil
}

func sleepCtx(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}
	select {
	case <-time.After(d):
	case <-ctx.Done():
	}
}

type renditionRef struct {
	uri  string // rendition directory, e.g. "v1"
	kbps int
}

func fetchMaster(ctx context.Context, client *http.Client, base string) ([]renditionRef, error) {
	body, _, err := get(ctx, client, base+"/master.m3u8")
	if err != nil {
		return nil, err
	}
	var out []renditionRef
	var kbps int
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF:") {
			for _, attr := range strings.Split(strings.TrimPrefix(line, "#EXT-X-STREAM-INF:"), ",") {
				if v, ok := strings.CutPrefix(attr, "BANDWIDTH="); ok {
					bw, _ := strconv.Atoi(v)
					kbps = bw / 1000
				}
			}
		} else if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, renditionRef{uri: strings.TrimSuffix(line, "/live.m3u8"), kbps: kbps})
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("master playlist had no renditions")
	}
	return out, nil
}

type playlist struct {
	firstSeq int
	segments []struct {
		seq  int
		name string
	}
}

func fetchPlaylist(ctx context.Context, client *http.Client, base, rendition string) (playlist, error) {
	body, _, err := get(ctx, client, base+"/"+rendition+"/live.m3u8")
	if err != nil {
		return playlist{}, err
	}
	var pl playlist
	seq := 0
	sc := bufio.NewScanner(strings.NewReader(string(body)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if v, ok := strings.CutPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"); ok {
			pl.firstSeq, _ = strconv.Atoi(v)
			seq = pl.firstSeq
		} else if line != "" && !strings.HasPrefix(line, "#") {
			pl.segments = append(pl.segments, struct {
				seq  int
				name string
			}{seq: seq, name: line})
			seq++
		}
	}
	return pl, nil
}

func fetchSegment(ctx context.Context, client *http.Client, base, rendition, name string) (int, int64, error) {
	body, hdr, err := get(ctx, client, base+"/"+rendition+"/"+name)
	if err != nil {
		return 0, 0, err
	}
	created, _ := strconv.ParseInt(hdr.Get(createdHeader), 10, 64)
	return len(body), created, nil
}

func get(ctx context.Context, client *http.Client, url string) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	return body, resp.Header, err
}
