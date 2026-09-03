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
	"syscall"

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
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runs < ablation.MinRuns {
		return fmt.Errorf("runs must be at least %d: a single run of a chaotic system is an anecdote", ablation.MinRuns)
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

	fmt.Fprintf(os.Stderr,
		"Artefact schema, the fixed-parameter guard, the chaos scheduler and all four\n"+
			"consumer modes are complete and tested. What is missing is the orchestration\n"+
			"that starts the ledger per configuration, drives load through it and collects\n"+
			"the measurements. See docs/ship-report.md.\n"+
			"Would write %d runs per configuration to %s\n", *runs, *out)
	return errors.New("ablation execution path not implemented (T-048)")
}
