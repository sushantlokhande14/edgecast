// Package webrtcpath is the WebRTC reference path: a Pion-based SFU with
// HTTP offer/answer signaling, a synthetic RTP publisher, and viewer
// sessions measured by the shared loadgen recorder.
package webrtcpath

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/sushantlokhande14/edgecast/internal/admin"
)

var (
	sfuPacketsIn = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecast_sfu_packets_in_total", Help: "RTP packets from the publisher",
	})
	sfuBytesIn = promauto.NewCounter(prometheus.CounterOpts{
		Name: "edgecast_sfu_bytes_in_total", Help: "RTP payload bytes from the publisher",
	})
	sfuSubscribers = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "edgecast_sfu_subscribers", Help: "Connected viewer PeerConnections",
	})
	sfuPublisherUp = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "edgecast_sfu_publisher_connected", Help: "1 while a publisher is connected",
	})
)

type sdpBody struct {
	SDP string `json:"sdp"`
}

// SFU forwards the publisher's RTP track to every viewer PeerConnection via
// a single shared TrackLocalStaticRTP (Pion fans WriteRTP out per binding).
type SFU struct {
	mu    sync.Mutex
	track *webrtc.TrackLocalStaticRTP
}

func NewSFU() *SFU { return &SFU{} }

func (s *SFU) localTrack() *webrtc.TrackLocalStaticRTP {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.track
}

func (s *SFU) RegisterHandlers(a *admin.Server) {
	a.Handle("/publish", s.handlePublish)
	a.Handle("/subscribe", s.handleSubscribe)
}

func (s *SFU) handlePublish(w http.ResponseWriter, r *http.Request) {
	offer, ok := readOffer(w, r)
	if !ok {
		return
	}
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		local, err := webrtc.NewTrackLocalStaticRTP(remote.Codec().RTPCodecCapability, "video", "edgecast")
		if err != nil {
			log.Printf("sfu: create local track: %v", err)
			return
		}
		s.mu.Lock()
		s.track = local
		s.mu.Unlock()
		sfuPublisherUp.Set(1)
		defer sfuPublisherUp.Set(0)
		log.Printf("sfu: publisher track up (%s)", remote.Codec().MimeType)
		for {
			pkt, _, err := remote.ReadRTP()
			if err != nil {
				log.Printf("sfu: publisher track ended: %v", err)
				return
			}
			sfuPacketsIn.Inc()
			sfuBytesIn.Add(float64(len(pkt.Payload)))
			if err := local.WriteRTP(pkt); err != nil {
				log.Printf("sfu: fanout write: %v", err)
			}
		}
	})
	answer, err := completeOfferAnswer(pc, offer)
	if err != nil {
		pc.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeAnswer(w, answer)
}

func (s *SFU) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	offer, ok := readOffer(w, r)
	if !ok {
		return
	}
	// Wait briefly for the publisher on cold start.
	var track *webrtc.TrackLocalStaticRTP
	for i := 0; i < 100; i++ {
		if track = s.localTrack(); track != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if track == nil {
		http.Error(w, "no publisher yet", http.StatusServiceUnavailable)
		return
	}
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sender, err := pc.AddTrack(track)
	if err != nil {
		pc.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go func() { // drain RTCP so interceptors run
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
		}
	}()
	sfuSubscribers.Inc()
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		if st == webrtc.PeerConnectionStateFailed || st == webrtc.PeerConnectionStateClosed {
			sfuSubscribers.Dec()
			pc.Close()
		}
	})
	answer, err := completeOfferAnswer(pc, offer)
	if err != nil {
		pc.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeAnswer(w, answer)
}

func readOffer(w http.ResponseWriter, r *http.Request) (webrtc.SessionDescription, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return webrtc.SessionDescription{}, false
	}
	var body sdpBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SDP == "" {
		http.Error(w, "body must be {sdp}", http.StatusBadRequest)
		return webrtc.SessionDescription{}, false
	}
	return webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: body.SDP}, true
}

// completeOfferAnswer runs the non-trickle answer flow: set remote offer,
// create the answer, wait for ICE gathering so candidates are inline.
func completeOfferAnswer(pc *webrtc.PeerConnection, offer webrtc.SessionDescription) (string, error) {
	if err := pc.SetRemoteDescription(offer); err != nil {
		return "", err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", err
	}
	done := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return "", err
	}
	<-done
	return pc.LocalDescription().SDP, nil
}

func writeAnswer(w http.ResponseWriter, sdp string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sdpBody{SDP: sdp})
}
