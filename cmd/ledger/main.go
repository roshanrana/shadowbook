// Command ledger is the SHADOWBOOK shadow ledger.
//
// One process, four internal loops: the HTTP API, the outbox relay, the
// movement consumer and the invariant checker. Every loop takes a context and
// returns on cancellation; nothing here starts a goroutine without a way to
// stop it (CLAUDE.md), and shutdown drains rather than strands.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"

	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/broker"
	"github.com/roshanrana/shadowbook/internal/ledger/consumer"
	"github.com/roshanrana/shadowbook/internal/ledger/httpapi"
	"github.com/roshanrana/shadowbook/internal/ledger/obs"
	"github.com/roshanrana/shadowbook/internal/ledger/outbox"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("ledger exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		addr       = flag.String("addr", envOr("SHADOWBOOK_ADDR", ":8080"), "listen address")
		dsn        = flag.String("dsn", envOr("SHADOWBOOK_LEDGER_DSN", ""), "PostgreSQL DSN")
		mode       = flag.String("mode", envOr("SHADOWBOOK_CONSUMER_MODE", "C"), "consumer delivery mode A|B|C|D")
		principals = flag.String("principals", envOr("SHADOWBOOK_PRINCIPALS", "sim,eod,consumer"), "comma-separated allow-list")
		invEvery   = flag.Duration("invariant-interval", 500*time.Millisecond, "invariant check interval")
		brokers    = flag.String("brokers", envOr("SHADOWBOOK_BROKERS", ""), "comma-separated Kafka seed brokers; empty means the in-process fake")
		group      = flag.String("group", envOr("SHADOWBOOK_GROUP", ""), "consumer group id; defaults to shadowbook-<mode>")
	)
	flag.Parse()

	if *dsn == "" {
		return errors.New("no DSN: set SHADOWBOOK_LEDGER_DSN or pass -dsn (see docs/runbook.md)")
	}
	if !consumer.Mode(*mode).Valid() {
		return fmt.Errorf("consumer mode %q is not one of A, B, C, D", *mode)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, *dsn)
	if err != nil {
		return err
	}
	defer st.Close()

	applied, err := st.Migrate(ctx)
	if err != nil {
		return err
	}
	logger.Info("migrations applied", "count", applied)

	if err := consumer.EnsureSuspenseAccounts(ctx, st, bizdate.Date(2028, time.January, 1)); err != nil {
		return err
	}

	reg := prometheus.NewRegistry()
	metrics := obs.NewMetrics(reg)
	checker := obs.NewChecker(st, metrics, *invEvery)

	srv := &http.Server{
		Addr: *addr,
		Handler: httpapi.New(httpapi.Config{
			Store: st, Metrics: metrics, Checker: checker, Registry: reg,
			Logger: logger, Principals: strings.Split(*principals, ","),
		}).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// The broker is in-process unless one is configured. The relay and the
	// consumer are always running: their behaviour is what Finding 2 measures,
	// and a ledger that only wires them up under a flag would be a different
	// program from the one under test.
	prod, cons, kind, err := dialBroker(*brokers, *group, *mode)
	if err != nil {
		return err
	}
	defer func() { _ = prod.Close(); _ = cons.Close() }()

	relay := outbox.New(st, prod, outbox.Options{Topic: postingsTopic})
	movements, err := consumer.New(st, cons, consumer.Options{
		Mode: consumer.Mode(*mode), Topic: movementsTopic, Metrics: metrics,
	})
	if err != nil {
		return err
	}
	logger.Info("broker configured", "kind", kind, "mode", *mode)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { return checker.Run(gctx) })
	g.Go(func() error { return relay.Run(gctx) })
	g.Go(func() error { return movements.Run(gctx) })
	g.Go(func() error {
		logger.Info("listening", "addr", *addr, "consumer_mode", *mode)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	g.Go(func() error {
		<-gctx.Done()
		// Stop accepting, then drain in-flight work (FR-L10).
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(gctx), 15*time.Second)
		defer cancel()
		logger.Info("shutting down")
		return srv.Shutdown(shutdownCtx)
	})

	if err := g.Wait(); err != nil {
		return err
	}
	logger.Info("stopped cleanly")
	return nil
}

// The two topics of LLD §4.2. They are deliberately NOT the same topic: the
// relay publishes this ledger's own postings for observability, while the
// consumer reads movements produced by legacy-sim. The in-process fake polls
// every topic it holds, so a single-topic confusion would not show up there --
// only against a real broker, where a subscription is to a named topic.
const (
	postingsTopic  = "shadowbook.postings.v1"
	movementsTopic = "shadowbook.movements.v1"
)

// dialBroker returns the producer and consumer for this process.
//
// With no seed brokers the in-process fake is used, which is what every test
// and the Finding 1 demo want: one process, no infrastructure. With seed
// brokers the franz-go clients are used. The relay and the consumer are wired
// identically either way -- the only difference is what they are talking to,
// which is the point.
func dialBroker(seeds, group, mode string) (broker.Producer, broker.Consumer, string, error) {
	if strings.TrimSpace(seeds) == "" {
		bus := broker.NewFake()
		return bus, bus, "in-process fake", nil
	}
	addrs := strings.Split(seeds, ",")
	for i := range addrs {
		addrs[i] = strings.TrimSpace(addrs[i])
	}
	if group == "" {
		// Distinct per configuration: two modes sharing a group id would share
		// committed offsets, and each would appear to lose the records the
		// other consumed.
		group = "shadowbook-" + mode
	}

	prod, err := broker.NewKafkaProducer(broker.KafkaConfig{
		Seeds: addrs, ClientID: "shadowbook-relay-" + mode,
	})
	if err != nil {
		return nil, nil, "", err
	}
	cons, err := broker.NewKafkaConsumer(broker.KafkaConfig{
		Seeds: addrs, Group: group, Topics: []string{movementsTopic},
		ClientID: "shadowbook-consumer-" + mode,
	})
	if err != nil {
		_ = prod.Close()
		return nil, nil, "", err
	}
	return prod, cons, fmt.Sprintf("kafka %v group=%s", addrs, group), nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
