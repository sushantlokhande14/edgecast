package webrtcpath

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/sushantlokhande14/edgecast/internal/media"
)

var pubRTPBytes = promauto.NewCounter(prometheus.CounterOpts{
	Name: "edgecast_webrtc_pub_bytes_total", Help: "RTP payload bytes sent to the SFU",
})

const (
	rtpMTU       = 1200
	rtpClockRate = 90000 // ticks per second; ts = unix-ms * 90 for absolute e2e
)

type PubConfig struct {
	SFUAddr     string // host:port of SFU signaling/admin
	BitrateKbps int
	Media       media.Config
}

// RunPublisher pushes the synthetic track to the SFU as RTP, reconnecting on
// failure. Bitrate is fixed: the WebRTC path measures transport behavior
// without sender-side bandwidth estimation (a documented limitation).
func RunPublisher(ctx context.Context, cfg PubConfig) {
	for ctx.Err() == nil {
		err := publishOnce(ctx, cfg)
		if ctx.Err() != nil {
			return
		}
		log.Printf("webrtc-pub: session ended: %v; reconnecting", err)
		time.Sleep(2 * time.Second)
	}
}

func publishOnce(ctx context.Context, cfg PubConfig) error {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}
	defer pc.Close()

	track, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: rtpClockRate},
		"video", "edgecast")
	if err != nil {
		return err
	}
	sender, err := pc.AddTrack(track)
	if err != nil {
		return err
	}
	go func() {
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
		}
	}()

	connected := make(chan struct{})
	failed := make(chan struct{})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		switch st {
		case webrtc.PeerConnectionStateConnected:
			select {
			case <-connected:
			default:
				close(connected)
			}
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			select {
			case <-failed:
			default:
				close(failed)
			}
		}
	})

	if err := signal(ctx, nil, pc, "http://"+cfg.SFUAddr+"/publish"); err != nil {
		return err
	}
	select {
	case <-connected:
	case <-failed:
		return fmt.Errorf("peer connection failed during setup")
	case <-ctx.Done():
		return nil
	case <-time.After(15 * time.Second):
		return fmt.Errorf("timeout waiting for connection")
	}
	log.Printf("webrtc-pub: connected to SFU at %d kbps", cfg.BitrateKbps)

	gen := media.NewGenerator(cfg.Media, cfg.BitrateKbps)
	ticker := time.NewTicker(gen.FrameInterval())
	defer ticker.Stop()
	var seq uint16
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-failed:
			return fmt.Errorf("peer connection failed")
		case <-ticker.C:
		}
		f := gen.Next(time.Now())
		ts := uint32(uint64(time.Now().UnixMilli()) * 90) // wraps ~13h; fine per run
		payload := f.Payload
		for off := 0; off < len(payload); off += rtpMTU {
			end := off + rtpMTU
			if end > len(payload) {
				end = len(payload)
			}
			pkt := &rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					PayloadType:    96, // rewritten per binding by Pion
					SequenceNumber: seq,
					Timestamp:      ts,
					SSRC:           0x0EDCE0, // rewritten per binding by Pion
					Marker:         end == len(payload),
				},
				Payload: payload[off:end],
			}
			seq++
			if err := track.WriteRTP(pkt); err != nil {
				return err
			}
			pubRTPBytes.Add(float64(end - off))
		}
	}
}

// signal POSTs the local offer and applies the remote answer. client=nil uses
// the default (unimpaired) HTTP client; subscribers pass their impaired one
// so signaling pays the emulated link like every other protocol's join.
func signal(ctx context.Context, client *http.Client, pc *webrtc.PeerConnection, url string) error {
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return err
	}
	done := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return err
	}
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}

	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	body, err := json.Marshal(sdpBody{SDP: pc.LocalDescription().SDP})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("signaling %s: HTTP %d", url, resp.StatusCode)
	}
	var ans sdpBody
	if err := json.NewDecoder(resp.Body).Decode(&ans); err != nil {
		return err
	}
	return pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: ans.SDP})
}
