package webrtcpath

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"

	"github.com/sushantlokhande14/edgecast/internal/loadgen"
	"github.com/sushantlokhande14/edgecast/internal/netem"
)

// Subscribe runs one WebRTC viewer session. All of the session's UDP (ICE,
// DTLS, SRTP) flows through an impaired PacketConn via Pion's ICE UDP mux,
// and signaling uses the impaired TCP dialer, so the join and the media both
// pay the emulated access link.
func Subscribe(ctx context.Context, link *netem.State, sfuAddr string, rec *loadgen.Recorder) error {
	sock, err := net.ListenUDP("udp", nil)
	if err != nil {
		return err
	}
	ipc := netem.NewPacketConn(sock, link)
	defer ipc.Close()

	se := webrtc.SettingEngine{}
	se.SetICEUDPMux(webrtc.NewICEUDPMux(nil, ipc))
	me := &webrtc.MediaEngine{}
	if err := me.RegisterDefaultCodecs(); err != nil {
		return err
	}
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(me, ir); err != nil {
		return err
	}
	api := webrtc.NewAPI(
		webrtc.WithSettingEngine(se),
		webrtc.WithMediaEngine(me),
		webrtc.WithInterceptorRegistry(ir),
	)
	pc, err := api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		return err
	}
	defer pc.Close()
	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		return err
	}

	done := make(chan error, 2)
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			pkt, _, err := tr.ReadRTP()
			if err != nil {
				done <- fmt.Errorf("track read: %w", err)
				return
			}
			now := time.Now()
			// Publisher stamps ts = unix-ms * 90; uint32 subtraction is
			// wrap-safe within a run.
			nowTicks := uint32(uint64(now.UnixMilli()) * 90)
			diff := nowTicks - pkt.Timestamp
			e2eMicros := uint64(diff) * 1000 / 90
			pts := uint64(now.UnixMicro()) - e2eMicros
			rec.Media(now, pts, len(pkt.Payload))
		}
	})
	pc.OnConnectionStateChange(func(st webrtc.PeerConnectionState) {
		if st == webrtc.PeerConnectionStateFailed || st == webrtc.PeerConnectionStateClosed {
			done <- fmt.Errorf("peer connection %s", st)
		}
	})

	sigClient := &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{DialContext: link.DialContext, DisableKeepAlives: true},
	}
	if err := signal(ctx, sigClient, pc, "http://"+sfuAddr+"/subscribe"); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return nil
	case err := <-done:
		return err
	}
}
