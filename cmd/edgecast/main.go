// Command edgecast is every role in the testbed, selected by subcommand.
// One binary and one image keep 20+ containers cheap to build and identical
// to operate: every role serves /metrics and /healthz on ADMIN_ADDR.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/sushantlokhande14/edgecast/internal/admin"
	"github.com/sushantlokhande14/edgecast/internal/loadgen"
	"github.com/sushantlokhande14/edgecast/internal/media"
	"github.com/sushantlokhande14/edgecast/internal/moqclient"
	"github.com/sushantlokhande14/edgecast/internal/relay"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	adm := admin.New()
	role := os.Args[1]
	var run func(context.Context, *admin.Server) error
	switch role {
	case "relay":
		run = runRelay
	case "moq-pub":
		run = runMoqPub
	case "moq-sub":
		run = runMoqSub
	default:
		fmt.Fprintf(os.Stderr, "unknown role %q\n", role)
		usage()
		os.Exit(2)
	}
	adm.Start(env("ADMIN_ADDR", ":8080"))
	if err := run(ctx, adm); err != nil && ctx.Err() == nil {
		log.Fatalf("%s: %v", role, err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: edgecast <role>
roles: relay | moq-pub | moq-sub
configuration is env-var driven; see docs/04-setup.md`)
}

func runRelay(ctx context.Context, adm *admin.Server) error {
	r := relay.New(env("REGION", "local"), env("UPSTREAM", ""))
	return r.ListenAndServe(ctx, env("LISTEN", ":4443"))
}

func runMoqPub(ctx context.Context, adm *admin.Server) error {
	cfg := moqclient.PubConfig{
		RelayAddr: env("RELAY_ADDR", "relay-us-west:4443"),
		Track:     env("TRACK", "cam0"),
		StartKbps: envInt("BITRATE_KBPS", 1000),
		ABR:       envBool("ABR", true),
		Media: media.Config{
			FPS:         envInt("FPS", 30),
			GroupFrames: envInt("GROUP_FRAMES", 30),
			KeyframeMul: 4.0,
		},
	}
	moqclient.RunPublisher(ctx, cfg)
	return nil
}

func runMoqSub(ctx context.Context, adm *admin.Server) error {
	relayAddr := env("RELAY_ADDR", "relay-us-west:4443")
	trackName := env("TRACK", "cam0")
	mgr := loadgen.NewManager("moq", env("REGION", "local"), envInt("SESSIONS", 10),
		func(ctx context.Context, rec *loadgen.Recorder) error {
			return moqclient.Subscribe(ctx, relayAddr, trackName, rec)
		})
	mgr.RegisterHandlers(adm)
	mgr.Start(ctx)
	<-ctx.Done()
	return nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			log.Fatalf("env %s=%q: not an integer", key, v)
		}
		return n
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			log.Fatalf("env %s=%q: not a bool", key, v)
		}
		return b
	}
	return def
}
