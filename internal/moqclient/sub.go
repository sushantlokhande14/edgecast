package moqclient

import (
	"bufio"
	"context"
	"errors"
	"io"
	"time"

	"github.com/sushantlokhande14/edgecast/internal/loadgen"
	"github.com/sushantlokhande14/edgecast/internal/netem"
	"github.com/sushantlokhande14/edgecast/internal/proto"
	"github.com/sushantlokhande14/edgecast/internal/quicutil"
)

// Subscribe runs one MoQ viewer session against a relay until ctx ends or the
// connection fails. The dial goes through the emulated access link; session
// start time (dial included) is owned by the Recorder, so startup delay
// covers the full join experience.
func Subscribe(ctx context.Context, link *netem.State, relayAddr, trackName string, rec *loadgen.Recorder) error {
	conn, pc, err := netem.DialQUIC(ctx, link, relayAddr, quicutil.ClientTLS(proto.ALPN), quicutil.Config())
	if err != nil {
		return err
	}
	defer pc.Close()
	defer conn.CloseWithError(0, "sub done")

	ctrl, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	if err := proto.WriteControl(ctrl, proto.Control{Type: proto.MsgSetup, Role: proto.RoleSubscriber}); err != nil {
		return err
	}
	if err := proto.WriteControl(ctrl, proto.Control{Type: proto.MsgSubscribe, Track: trackName}); err != nil {
		return err
	}
	if _, err := proto.ReadControl(ctrl); err != nil {
		return err
	}

	for {
		st, err := conn.AcceptUniStream(ctx)
		if err != nil {
			return err
		}
		go func() {
			br := bufio.NewReaderSize(st, 64<<10)
			if _, _, err := proto.ReadGroupHeader(br); err != nil {
				return
			}
			for {
				h, payload, err := proto.ReadObject(br)
				if err != nil {
					if !errors.Is(err, io.EOF) {
						_ = err // aborted group; bytes already counted
					}
					return
				}
				rec.Media(time.Now(), h.PTSMicros, len(payload))
			}
		}()
	}
}
