// Package proto defines the simplified MoQ-style wire protocol.
//
// Control plane: one bidirectional QUIC stream per session carrying
// length-prefixed JSON messages. Real moq-transport uses varint-coded binary;
// JSON is a deliberate testbed choice for debuggability.
//
// Data plane: each group (GOP) rides its own unidirectional QUIC stream, so
// loss in one group cannot head-of-line-block the next. The stream opens with
// a header (track name, group seq) followed by binary-framed objects.
package proto

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ALPN for all MoQ-style QUIC connections in the testbed.
const ALPN = "edgecast-moq"

// Control message types.
const (
	MsgSetup       = "setup"
	MsgAnnounce    = "announce"
	MsgAnnounceOK  = "announce_ok"
	MsgSubscribe   = "subscribe"
	MsgSubscribeOK = "subscribe_ok"
	MsgError       = "error"
)

// Session roles carried in SETUP.
const (
	RolePublisher  = "publisher"
	RoleSubscriber = "subscriber"
	RoleRelay      = "relay" // a downstream relay pulling through; treated as a subscriber
)

type Control struct {
	Type     string `json:"type"`
	Role     string `json:"role,omitempty"`
	Track    string `json:"track,omitempty"`
	GroupSeq uint64 `json:"group_seq,omitempty"`
	Message  string `json:"message,omitempty"`
}

const maxControlBytes = 1 << 16

func WriteControl(w io.Writer, c Control) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func ReadControl(r io.Reader) (Control, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Control{}, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > maxControlBytes {
		return Control{}, fmt.Errorf("control frame size %d out of range", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return Control{}, err
	}
	var c Control
	err := json.Unmarshal(b, &c)
	return c, err
}

// ObjectHeader describes one object (frame) on a group stream. GroupSeq is
// carried once in the stream header, not per object; readers fill it in.
type ObjectHeader struct {
	GroupSeq  uint64
	ObjectSeq uint64
	PTSMicros uint64
	Keyframe  bool
}

// WriteGroupHeader begins a unidirectional group stream.
func WriteGroupHeader(w io.Writer, track string, groupSeq uint64) error {
	buf := make([]byte, 0, len(track)+16)
	buf = binary.AppendUvarint(buf, uint64(len(track)))
	buf = append(buf, track...)
	buf = binary.AppendUvarint(buf, groupSeq)
	_, err := w.Write(buf)
	return err
}

func ReadGroupHeader(r *bufio.Reader) (track string, groupSeq uint64, err error) {
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return "", 0, err
	}
	if n > 1024 {
		return "", 0, errors.New("track name too long")
	}
	tb := make([]byte, n)
	if _, err := io.ReadFull(r, tb); err != nil {
		return "", 0, unexpected(err)
	}
	g, err := binary.ReadUvarint(r)
	if err != nil {
		return "", 0, unexpected(err)
	}
	return string(tb), g, nil
}

// EncodeObject frames one object (header + payload) into a single byte slice,
// ready to write to a stream or fan out to many.
func EncodeObject(h ObjectHeader, payload []byte) []byte {
	buf := make([]byte, 0, len(payload)+24)
	buf = binary.AppendUvarint(buf, h.ObjectSeq)
	buf = binary.AppendUvarint(buf, h.PTSMicros)
	var flags byte
	if h.Keyframe {
		flags |= 1
	}
	buf = append(buf, flags)
	buf = binary.AppendUvarint(buf, uint64(len(payload)))
	buf = append(buf, payload...)
	return buf
}

const maxObjectBytes = 8 << 20

// ReadObject reads one object from a group stream. A clean end of stream
// (FIN at an object boundary) returns io.EOF.
func ReadObject(r *bufio.Reader) (ObjectHeader, []byte, error) {
	var h ObjectHeader
	seq, err := binary.ReadUvarint(r)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return h, nil, io.EOF
		}
		return h, nil, err
	}
	h.ObjectSeq = seq
	pts, err := binary.ReadUvarint(r)
	if err != nil {
		return h, nil, unexpected(err)
	}
	h.PTSMicros = pts
	flags, err := r.ReadByte()
	if err != nil {
		return h, nil, unexpected(err)
	}
	h.Keyframe = flags&1 != 0
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return h, nil, unexpected(err)
	}
	if n > maxObjectBytes {
		return h, nil, fmt.Errorf("object of %d bytes exceeds limit", n)
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return h, nil, unexpected(err)
	}
	return h, p, nil
}

func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
