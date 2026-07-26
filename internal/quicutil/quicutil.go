// Package quicutil centralizes QUIC and TLS setup so every role uses the same
// transport parameters. The testbed uses a self-signed certificate generated
// at process start and clients skip verification; real deployments would not.
package quicutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"time"

	"github.com/quic-go/quic-go"
)

func ServerTLS(alpn string) (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "edgecast"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		DNSNames:     []string{"edgecast", "localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	return &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{alpn}}, nil
}

func ClientTLS(alpn string) *tls.Config {
	return &tls.Config{InsecureSkipVerify: true, NextProtos: []string{alpn}}
}

// Config returns the QUIC transport parameters shared by all roles. Uni
// stream limits are high because every media group consumes one stream.
func Config() *quic.Config {
	return &quic.Config{
		MaxIdleTimeout:                 30 * time.Second,
		KeepAlivePeriod:                10 * time.Second,
		MaxIncomingStreams:             256,
		MaxIncomingUniStreams:          1 << 14,
		InitialStreamReceiveWindow:     512 * 1024,
		MaxStreamReceiveWindow:         8 * 1024 * 1024,
		InitialConnectionReceiveWindow: 1024 * 1024,
		MaxConnectionReceiveWindow:     16 * 1024 * 1024,
	}
}
