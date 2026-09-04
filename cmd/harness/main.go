// Command harness drives load, chaos and the delivery-semantics ablation.
//
// It refuses to produce an artefact it cannot stand behind: no Docker daemon,
// no ablation, and a clear message saying so rather than a table of numbers
// measured against something that was not the experiment.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/roshanrana/shadowbook/internal/harness/ablation"
	"github.com/roshanrana/shadowbook/internal/harness/chaos"
	"github.com/roshanrana/shadowbook/internal/ledger/consumer"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "harness: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: harness <ablate|fold|preflight> [flags]")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "preflight":
		return preflight(ctx)
	case "ablate":
		return ablate(ctx, args[1:])
	case "fold":
		return fold(args[1:])
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
	simulated := fs.Bool("simulated", false,
		"run against an in-process multi-broker cluster instead of Docker; results are labelled simulated and can never render as Finding 2")
	simBrokers := fs.Int("sim-brokers", 3, "brokers in the simulated cluster")
	configs := fs.String("configs", "A,B,C", "comma-separated delivery modes to run")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runs < ablation.MinRuns {
		return fmt.Errorf("runs must be at least %d: a single run of a chaotic system is an anecdote", ablation.MinRuns)
	}
	if *dsn == "" {
		return errors.New("no DSN: set SHADOWBOOK_LEDGER_DSN or pass -dsn")
	}

	// The schedule is scaled to the run length so a shorter sweep executes the
	// same shape rather than an improvised one.
	schedule := chaos.Scale(chaos.DefaultSchedule(), *duration)
	if err := chaos.Validate(schedule); err != nil {
		return err
	}

	var cluster ablation.Cluster
	if *simulated {
		sim, err := ablation.NewSimCluster(*simBrokers)
		if err != nil {
			return err
		}
		defer sim.Close()
		fmt.Fprintf(os.Stderr,
			"SIMULATED CLUSTER: %d in-process brokers on real sockets, killed and\n"+
				"restarted on the chaos schedule. This measures the delivery modes under\n"+
				"broker failover, but there is no replication, no ISR and no disk, so it\n"+
				"is NOT Finding 2 and `make report` will label it as simulated.\n\n",
			*simBrokers)
		cluster = sim
	} else {
		cli := chaos.NewCLI()
		if ok, detail := cli.Available(ctx); !ok {
			fmt.Fprintln(os.Stderr,
				"The ablation measures a real three-broker cluster being killed under load.\n"+
					"Run `make up-chaos` on a machine with Docker, then `make ablate`.\n"+
					"For a runnable approximation with no Docker, use `make ablate-sim`,\n"+
					"whose results are labelled simulated and never render as Finding 2.")
			return fmt.Errorf("no docker daemon: %s", detail)
		}
		cluster = ablation.RealCluster{
			Addrs: strings.Split(*brokers, ","), Ver: *brokerVersion, Control: cli,
		}
	}

	r := &ablation.Runner{
		Cluster: cluster,
		Logf:    func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) },
		Spec: ablation.Spec{
			Configs: parseModes(*configs),
			Runs:    *runs, Seed: *seed, Profile: "payday",
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

// fold turns run artefacts into the single JSON file `make report` renders
// from, and is deliberately a separate step from running them.
//
// The report must never touch a live system: everything it states has to come
// from artefacts on disk, so a report can be regenerated months later from a
// directory of files and say exactly what it said the first time.
func fold(args []string) error {
	fs := flag.NewFlagSet("fold", flag.ContinueOnError)
	in := fs.String("in", "reports/runs", "artefact directory")
	out := fs.String("out", "reports/runs/finding2.json", "rendered input for make report")
	minRuns := fs.Int("min-runs", ablation.MinRuns, "minimum runs per configuration")
	if err := fs.Parse(args); err != nil {
		return err
	}

	arts, err := ablation.Load(*in)
	if err != nil {
		return err
	}
	payload := map[string]any{"runs_per_config": *minRuns}

	rows, tableErr := ablation.Table(arts, *minRuns)
	if tableErr != nil {
		// Not a failure. "Finding 2 could not be built, and here is precisely
		// why" is a legitimate and more useful output than a missing file, and
		// the report renders it verbatim.
		payload["rows"] = nil
		payload["reason"] = tableErr.Error()
	} else {
		kind, err := ablation.KindOf(arts)
		if err != nil {
			return err
		}
		payload["rows"] = rows
		payload["kind"] = string(kind)
		payload["broker"] = arts[0].BrokerVersion
		payload["exact_counts"] = allExact(arts)
	}

	blob, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(*out, append(blob, '\n'), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "folded %d artefacts into %s\n", len(arts), *out)
	return nil
}

func allExact(arts []ablation.Artefact) bool {
	for _, a := range arts {
		if !a.ExactCounts {
			return false
		}
	}
	return true
}

func parseModes(s string) []consumer.Mode {
	var out []consumer.Mode
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, consumer.Mode(part))
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
