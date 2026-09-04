// Command harness drives load, chaos and the delivery-semantics ablation.
//
// It refuses to produce an artefact it cannot stand behind: no Docker daemon,
// no ablation, and a clear message saying so rather than a table of numbers
// measured against something that was not the experiment.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/roshanrana/shadowbook/internal/harness/ablation"
	"github.com/roshanrana/shadowbook/internal/harness/chaos"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "harness: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: harness <ablate|preflight> [flags]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "preflight":
		return preflight(ctx)
	case "ablate":
		return ablate(ctx, args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

// preflight reports whether an ablation could run here, and why not if not.
func preflight(ctx context.Context) error {
	cli := chaos.NewCLI()
	ok, detail := cli.Available(ctx)
	if !ok {
		fmt.Println("docker:        UNAVAILABLE")
		if detail != "" {
			fmt.Printf("               %s\n", detail)
		}
		fmt.Println()
		fmt.Println("The ablation needs the three-broker chaos profile. Without it,")
		fmt.Println("`make report` renders Finding 2 as 'not run' -- which is correct,")
		fmt.Println("not a failure. The mechanism each configuration relies on is still")
		fmt.Println("demonstrated by the consumer tests; only the numbers are missing.")
		return errors.New("docker daemon unavailable")
	}
	fmt.Printf("docker:        ok (server %s)\n", detail)
	if err := chaos.Validate(chaos.DefaultSchedule()); err != nil {
		return err
	}
	fmt.Printf("schedule:      ok (%d events, quorum preserved throughout)\n", len(chaos.DefaultSchedule()))
	fmt.Printf("min runs:      %d per configuration\n", ablation.MinRuns)
	return nil
}

func ablate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ablate", flag.ContinueOnError)
	runs := fs.Int("runs", ablation.MinRuns, "runs per configuration")
	out := fs.String("out", "reports/runs", "artefact directory")
	brokers := fs.String("brokers", envOr("SHADOWBOOK_BROKERS", "localhost:19092,localhost:29092,localhost:39092"),
		"comma-separated Kafka seed brokers")
	brokerVersion := fs.String("broker-version", envOr("SHADOWBOOK_BROKER_VERSION", "redpanda v24.3.6"),
		"recorded in every artefact; runs against different broker builds are not comparable")
	dsn := fs.String("dsn", envOr("SHADOWBOOK_LEDGER_DSN", ""), "admin DSN; each run gets its own database")
	binary := fs.String("ledger", envOr("SHADOWBOOK_LEDGER_BIN", "bin/ledger"), "compiled ledger binary")
	seed := fs.Int64("seed", 20260904, "fixed across every run in a table")
	rate := fs.Int("rate", 1000, "movements per second (NFR-1a)")
	duration := fs.Duration("duration", 4*time.Minute, "load duration per run")
	sha := fs.String("ledger-sha", "", "recorded in every artefact")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runs < ablation.MinRuns {
		return fmt.Errorf("runs must be at least %d: a single run of a chaotic system is an anecdote", ablation.MinRuns)
	}
	if *dsn == "" {
		return errors.New("no DSN: set SHADOWBOOK_LEDGER_DSN or pass -dsn")
	}

	cli := chaos.NewCLI()
	if ok, detail := cli.Available(ctx); !ok {
		fmt.Fprintln(os.Stderr,
			"The ablation measures a real three-broker cluster being killed under load;\n"+
				"there is no meaningful way to fake it. Run `make up-chaos` on a machine\n"+
				"with Docker, then `make ablate`. Until then `make report` correctly renders\n"+
				"Finding 2 as 'not run'")
		return fmt.Errorf("no docker daemon: %s", detail)
	}

	schedule := chaos.DefaultSchedule()
	if err := chaos.Validate(schedule); err != nil {
		return err
	}

	r := &ablation.Runner{
		Cluster: ablation.RealCluster{
			Addrs: strings.Split(*brokers, ","), Ver: *brokerVersion, Control: cli,
		},
		Logf: func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) },
		Spec: ablation.Spec{
			Runs: *runs, Seed: *seed, Profile: "payday",
			Rate: *rate, Duration: *duration, Schedule: schedule,
			AdminDSN: *dsn, LedgerBinary: *binary, OutDir: *out, LedgerSHA: *sha,
		},
	}
	arts, err := r.Run(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %d artefacts to %s\n", len(arts), *out)

	// Fold immediately. A sweep that cannot become a table should say so now,
	// while the cluster is still up and the run can be repeated, rather than at
	// `make report` time after everything has been torn down.
	if _, err := ablation.Table(arts, ablation.MinRuns); err != nil {
		return fmt.Errorf("artefacts written, but they do not form a table: %w", err)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
