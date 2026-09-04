//go:build integration

package ablation_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"

	"github.com/roshanrana/shadowbook/internal/harness/ablation"
	"github.com/roshanrana/shadowbook/internal/harness/chaos"
	"github.com/roshanrana/shadowbook/internal/ledger/consumer"
)

// This exercises the ablation orchestration end to end -- provision a database
// per run, start the ledger as a separate process pointed at a real broker,
// drive movements over the wire, wait for the drain to settle, measure out of
// the database, and write an artefact.
//
// It is a HARNESS CHECK, not Finding 2. kfake is one in-process broker and
// cannot be killed mid-write, so the loss and duplication columns here describe
// the plumbing. The final assertion is the important one: these artefacts must
// be REFUSED by Table, so a green run of this test can never be mistaken for a
// measurement.

type fakeCluster struct{ seeds []string }

func (f fakeCluster) Seeds() []string      { return f.seeds }
func (f fakeCluster) Version() string      { return ablation.BrokerFake + "kfake" }
func (f fakeCluster) Docker() chaos.Docker { return nil }

// Replicas is 1: a single in-process broker cannot replicate anything.
func (f fakeCluster) Replicas() int16 { return 1 }

func adminDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("SHADOWBOOK_LEDGER_DSN")
	if dsn == "" {
		t.Skip("SHADOWBOOK_LEDGER_DSN unset; run `make up` first")
	}
	return dsn
}

// buildLedger compiles the ledger once for this test binary.
func buildLedger(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ledger")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/ledger")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build ledger: %v\n%s", err, out)
	}
	return bin
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find repo root")
	return ""
}

func TestRunnerProducesArtefactsThatAreRefusedAsAFinding(t *testing.T) {
	if testing.Short() {
		t.Skip("starts processes and a broker")
	}
	dsn := adminDSN(t)

	cluster, err := kfake.NewCluster(
		kfake.NumBrokers(1),
		// The runner uses a distinct topic per run, so they cannot be seeded
		// up front; the producer creates each on first write.
		kfake.AllowAutoTopicCreation(),
		kfake.SeedTopics(1, "shadowbook.postings.v1"),
		kfake.GroupMinSessionTimeout(100*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("kfake: %v", err)
	}
	defer cluster.Close()

	out := t.TempDir()
	r := &ablation.Runner{
		Cluster: fakeCluster{seeds: cluster.ListenAddrs()},
		Logf:    t.Logf,
		Spec: ablation.Spec{
			// One configuration, MinRuns times: enough to prove the loop and
			// the artefact set, without paying for three modes.
			Configs:      []consumer.Mode{consumer.InboxDedup},
			Runs:         ablation.MinRuns,
			Seed:         20260904,
			Profile:      "steady",
			Rate:         200,
			Duration:     2 * time.Second,
			AdminDSN:     dsn,
			LedgerBinary: buildLedger(t),
			OutDir:       out,
			LedgerSHA:    "test",
			Accounts:     8,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	arts, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(arts) != ablation.MinRuns {
		t.Fatalf("got %d artefacts, want %d", len(arts), ablation.MinRuns)
	}

	for _, a := range arts {
		if a.Sent == 0 {
			t.Fatalf("run %s sent nothing; the driver did not drive", a.RunID)
		}
		if a.Applied == 0 {
			t.Fatalf("run %s applied nothing; the ledger consumed nothing over the "+
				"broker, so this test proved only that processes start", a.RunID)
		}
		if !a.InvariantHeld {
			t.Fatalf("run %s broke the zero-sum invariant", a.RunID)
		}
		// Mode C dedups in the inbox, so no movement may be applied twice even
		// though every record crossed a real broker.
		if a.Duplicated != 0 {
			t.Fatalf("run %s duplicated %d movements under mode C", a.RunID, a.Duplicated)
		}
	}

	// Artefacts must be on disk, not merely returned: `make report` reads the
	// directory and never sees the in-memory values.
	reloaded, err := ablation.Load(out)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(reloaded) != len(arts) {
		t.Fatalf("loaded %d artefacts from disk, want %d", len(reloaded), len(arts))
	}

	// The point of the whole test: a green harness check is still not a finding.
	if _, err := ablation.Table(reloaded, ablation.MinRuns); err == nil {
		t.Fatal("Table accepted kfake artefacts as a finding")
	} else {
		var notMeasurement *ablation.ErrNotAMeasurement
		if !errors.As(err, &notMeasurement) {
			t.Fatalf("err = %v, want ErrNotAMeasurement", err)
		}
	}
}
