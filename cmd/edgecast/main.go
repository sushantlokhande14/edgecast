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
	"github.com/sushantlokhande14/edgecast/internal/expctl"
	"github.com/sushantlokhande14/edgecast/internal/loadgen"
	"github.com/sushantlokhande14/edgecast/internal/media"
	"github.com/sushantlokhande14/edgecast/internal/moqclient"
	"github.com/sushantlokhande14/edgecast/internal/netem"
	"github.com/sushantlokhande14/edgecast/internal/relay"
)

// link is the process-wide emulated access/backbone link. Every role gets
// one: base RTT from REGION_RTT_MS plus whatever scenario expctl applies.
var link *netem.State

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
	case "expctl":
		run = func(ctx context.Context, _ *admin.Server) error {
			return expctl.Run(ctx, env("MATRIX", "/app/scenarios/smoke.yaml"), env("OUT", "/results"))
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown role %q\n", role)
		usage()
		os.Exit(2)
	}
	link = netem.NewState(envFloat("REGION_RTT_MS", 0))
	link.RegisterHandlers(adm)
	adm.Start(env("ADMIN_ADDR", ":8080"))
	if err := run(ctx, adm); err != nil && ctx.Err() == nil {
		log.Fatalf("%s: %v", role, err)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: edgecast <role>
roles: relay | moq-pub | moq-sub | expctl
configuration is env-var driven; see docs/04-setup.md`)
}

func runRelay(ctx context.Context, adm *admin.Server) error {
	r := relay.New(env("REGION", "local"), env("UPSTREAM", ""), link)
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
	moqclient.RunPublisher(ctx, link, cfg)
	return nil
}

func runMoqSub(ctx context.Context, adm *admin.Server) error {
	relayAddr := env("RELAY_ADDR", "relay-us-west:4443")
	trackName := env("TRACK", "cam0")
	mgr := loadgen.NewManager("moq", env("REGION", "local"), envInt("SESSIONS", 10),
		func(ctx context.Context, rec *loadgen.Recorder) error {
			return moqclient.Subscribe(ctx, link, relayAddr, trackName, rec)
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

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			log.Fatalf("env %s=%q: not a number", key, v)
		}
		return f
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
