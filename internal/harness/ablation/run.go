package ablation

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	shadowbookv1 "github.com/roshanrana/shadowbook/gen/go/shadowbook/v1"
	"github.com/roshanrana/shadowbook/internal/bizdate"
	"github.com/roshanrana/shadowbook/internal/broker"
	"github.com/roshanrana/shadowbook/internal/harness/chaos"
	"github.com/roshanrana/shadowbook/internal/ledger/consumer"
	"github.com/roshanrana/shadowbook/internal/ledger/store"
)

// Cluster is the broker under test.
//
// An interface with exactly two implementations: a real cluster, and an
// in-process one used to exercise this orchestration on a machine with no
// Docker. Version() is what keeps the two apart downstream -- an in-process
// cluster reports a version prefixed BrokerFake, and Table refuses it.
type Cluster interface {
	Seeds() []string
	Version() string
	// Docker is the container control surface for the chaos schedule, or nil
	// when this cluster cannot be killed. A nil Docker is not an error; it
	// means the run is a harness check rather than a measurement, which
	// Version() already encodes.
	Docker() chaos.Docker
}

// RealCluster is a running broker cluster that can be killed.
//
// Ver is supplied rather than probed, and is recorded in every artefact: two
// runs against different broker builds are not comparable, and the
// fixed-parameter guard is what enforces that.
type RealCluster struct {
	Addrs   []string
	Ver     string
	Control chaos.Docker
}

func (c RealCluster) Seeds() []string      { return c.Addrs }
func (c RealCluster) Version() string      { return c.Ver }
func (c RealCluster) Docker() chaos.Docker { return c.Control }

// Spec is one experiment: the fixed parameters, held identical across every
// configuration, plus where to run it.
type Spec struct {
	Configs  []consumer.Mode
	Runs     int
	Seed     int64
	Profile  string
	Rate     int
	Duration time.Duration
	Schedule []chaos.Event

	// AdminDSN points at a database the runner may create databases from. Each
	// run gets its own, because Applied and Duplicated are counts over a whole
	// database and a reused one would carry the previous run's rows.
	AdminDSN string
	// LedgerBinary is the compiled ledger. The consumer must be a separate
	// process with its own offset state (HLD): running it in-process here
	// would quietly change what is being measured.
	LedgerBinary string
	OutDir       string
	LedgerSHA    string

	// Accounts is how many distinct accounts movements are spread over.
	Accounts int
}

func (s *Spec) setDefaults() {
	if s.Runs <= 0 {
		s.Runs = MinRuns
	}
	if len(s.Configs) == 0 {
		s.Configs = []consumer.Mode{consumer.AtMostOnce, consumer.AtLeastOnce, consumer.InboxDedup}
	}
	if s.Profile == "" {
		s.Profile = "steady"
	}
	if s.Rate <= 0 {
		s.Rate = 200
	}
	if s.Duration <= 0 {
		s.Duration = 30 * time.Second
	}
	if s.Accounts <= 0 {
		s.Accounts = 20
	}
	if s.OutDir == "" {
		s.OutDir = "reports/runs"
	}
}

func (s Spec) validate() error {
	if s.Runs < MinRuns {
		return fmt.Errorf("ablation: %d runs per configuration; a single run of a "+
			"chaotic system is an anecdote, minimum is %d", s.Runs, MinRuns)
	}
	if s.AdminDSN == "" {
		return errors.New("ablation: no admin DSN to provision run databases from")
	}
	if s.LedgerBinary == "" {
		return errors.New("ablation: no ledger binary; build it with `go build ./cmd/ledger`")
	}
	if _, err := os.Stat(s.LedgerBinary); err != nil {
		return fmt.Errorf("ablation: ledger binary: %w", err)
	}
	for _, c := range s.Configs {
		if !c.Valid() {
			return fmt.Errorf("ablation: %q is not a delivery mode", c)
		}
	}
	return nil
}

// Runner executes a Spec against a Cluster.
type Runner struct {
	Cluster Cluster
	Spec    Spec
	// Logf receives progress. Nil is silent.
	Logf func(format string, args ...any)
	// Now is injected so the drain detector can be driven deterministically in
	// tests instead of depending on wall-clock timing.
	Now func() time.Time
}

func (r *Runner) logf(format string, args ...any) {
	if r.Logf != nil {
		r.Logf(format, args...)
	}
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Run executes every configuration the requested number of times and returns
// the artefacts, having written each to disk as it completes.
//
// Artefacts are written per run rather than at the end: a four-configuration
// sweep takes tens of minutes, and losing all of it because the last run failed
// would be its own kind of defect.
func (r *Runner) Run(ctx context.Context) ([]Artefact, error) {
	r.Spec.setDefaults()
	if err := r.Spec.validate(); err != nil {
		return nil, err
	}
	if r.Cluster == nil {
		return nil, errors.New("ablation: no cluster")
	}

	var out []Artefact
	for _, mode := range r.Spec.Configs {
		for i := 0; i < r.Spec.Runs; i++ {
			runID := fmt.Sprintf("%s-%s-%d", r.Spec.Profile, mode, i+1)
			r.logf("run %s starting", runID)

			art, err := r.one(ctx, mode, runID)
			if err != nil {
				return out, fmt.Errorf("ablation: run %s: %w", runID, err)
			}
			if _, err := art.Write(r.Spec.OutDir); err != nil {
				return out, err
			}
			r.logf("run %s: sent=%d applied=%d lost=%d duplicated=%d invariant=%v",
				runID, art.Sent, art.Applied, art.Lost, art.Duplicated, art.InvariantHeld)
			out = append(out, art)
		}
	}
	return out, nil
}

// one executes a single (configuration, run) pair.
func (r *Runner) one(ctx context.Context, mode consumer.Mode, runID string) (Artefact, error) {
	art := Artefact{
		RunID: runID, Config: mode,
		Seed: r.Spec.Seed, Profile: r.Spec.Profile,
		RatePerSec: r.Spec.Rate, DurationSec: int(r.Spec.Duration.Seconds()),
		Schedule: r.Spec.Schedule, LedgerSHA: r.Spec.LedgerSHA,
		BrokerVersion: r.Cluster.Version(),
	}

	dsn, drop, err := provision(ctx, r.Spec.AdminDSN, runID)
	if err != nil {
		return art, err
	}
	defer drop()

	// A distinct group AND a distinct topic per run.
	//
	// The group alone is not enough, and assuming it was cost a debugging pass:
	// a fresh group starts at the beginning of the topic, so run 2 replayed
	// every record run 1 had produced. Runs then contaminate each other's
	// counts, which is exactly the isolation failure the per-run database was
	// introduced to prevent -- the database was isolated and the log was not.
	group := "sb-" + strings.ToLower(runID)
	topic := "shadowbook.movements." + strings.ToLower(runID) + ".v1"

	st, err := store.Open(ctx, dsn)
	if err != nil {
		return art, fmt.Errorf("ablation: open run database: %w", err)
	}
	defer st.Close()

	// Migrate and seed BEFORE the ledger starts, not after it reports ready.
	// The consumer begins applying the moment it joins the group, and an
	// account it references has to exist by then; seeding afterwards is a race
	// that the consumer wins whenever the broker has anything to deliver.
	if _, err := st.Migrate(ctx); err != nil {
		return art, fmt.Errorf("ablation: migrate run database: %w", err)
	}
	if err := consumer.EnsureSuspenseAccounts(ctx, st, bizdate.Date(2028, time.January, 1)); err != nil {
		return art, err
	}
	accounts, err := seedAccounts(ctx, st, r.Spec.Accounts)
	if err != nil {
		return art, err
	}

	// Created before the ledger starts: a consumer subscribing to a topic that
	// does not exist yet spends its first seconds retrying metadata, which eats
	// into the measured window.
	if err := broker.EnsureTopic(ctx, r.Cluster.Seeds(), topic, 6, 1); err != nil {
		return art, err
	}

	stop, err := r.startLedger(ctx, dsn, mode, group, topic)
	if err != nil {
		return art, err
	}
	defer stop()

	prod, err := broker.NewKafkaProducer(broker.KafkaConfig{
		Seeds: r.Cluster.Seeds(), ClientID: "ablation-" + string(mode),
	})
	if err != nil {
		return art, err
	}
	defer func() { _ = prod.Close() }()

	// Chaos runs concurrently with load, on its own schedule, and is recorded
	// as EXECUTED rather than as intended.
	chaosDone := make(chan []chaos.Record, 1)
	loadCtx, cancelLoad := context.WithCancel(ctx)
	defer cancelLoad()
	go func() {
		if r.Cluster.Docker() == nil || len(r.Spec.Schedule) == 0 {
			chaosDone <- nil
			return
		}
		recs, _ := chaos.NewRunner(r.Cluster.Docker()).Run(loadCtx, r.Spec.Schedule)
		chaosDone <- recs
	}()

	sent, err := r.drive(loadCtx, prod, accounts, topic)
	art.Sent = sent
	if err != nil {
		return art, err
	}
	cancelLoad()
	art.Executed = <-chaosDone

	drainStart := r.now()
	if err := r.waitForDrain(ctx, st, sent); err != nil {
		r.logf("run %s: drain did not settle: %v", runID, err)
	}
	art.DrainSeconds = r.now().Sub(drainStart).Seconds()

	if err := measure(ctx, st, &art); err != nil {
		return art, err
	}
	return art, nil
}

// drive produces `rate * duration` movement events at a steady rate.
//
// Open-model: arrivals are placed by the clock, not by when the previous
// produce returned. A closed-model driver would slow down exactly when the
// broker was struggling, which is the moment the experiment is about.
func (r *Runner) drive(ctx context.Context, prod broker.Producer, accounts []uuid.UUID, topic string) (int64, error) {
	const batch = 50
	total := int64(r.Spec.Rate) * int64(r.Spec.Duration.Seconds())
	if total <= 0 {
		return 0, nil
	}
	interval := time.Duration(float64(time.Second) / float64(r.Spec.Rate) * batch)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	date := bizdate.Date(2028, time.February, 29)
	var sent int64
	for sent < total {
		select {
		case <-ctx.Done():
			return sent, nil
		case <-ticker.C:
		}
		n := min(int64(batch), total-sent)
		recs := make([]broker.Record, 0, n)
		for i := int64(0); i < n; i++ {
			seq := sent + i
			account := accounts[seq%int64(len(accounts))]
			// The message id is a deterministic function of (seed, sequence),
			// so a rerun of the same spec produces the same ids and the inbox
			// dedup path is exercised identically.
			msgID := fmt.Sprintf("mv-%d-%d", r.Spec.Seed, seq)
			ev := &shadowbookv1.MovementEvent{
				MessageId: msgID,
				AccountId: account.String(),
				Amount:    &shadowbookv1.Money{Minor: 1_000 + seq%9_000, Currency: "USD", Scale: 2},
				Kind:      "transfer",

				BusinessDate: date.String(),
				ValueDate:    date.String(),
				PostedAt:     time.Date(2028, time.February, 29, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
			}
			value, err := proto.Marshal(ev)
			if err != nil {
				return sent, err
			}
			recs = append(recs, broker.Record{
				Topic: topic, Key: account.String(), Value: value,
			})
		}
		if err := prod.Produce(ctx, recs); err != nil {
			// A produce that fails during chaos is a fact about the run, not a
			// reason to abandon it: those records are genuinely not sent, and
			// `sent` must not count them or Lost would be overstated.
			if ctx.Err() != nil {
				return sent, nil
			}
			// Failing on the very first batch is different in kind. Nothing has
			// been killed yet, so this is the experiment failing to start --
			// a missing topic, a bad address -- and reporting it as a run that
			// sent nothing would render as total loss, which is precisely the
			// result the experiment exists to measure. Surface it instead.
			if sent == 0 {
				return 0, fmt.Errorf("ablation: first produce failed, so the run never "+
					"started (this is a setup fault, not a measurement): %w", err)
			}
			r.logf("produce failed after %d records: %v", sent, err)
			return sent, nil
		}
		sent += n
	}
	return sent, nil
}

// waitForDrain blocks until the applied count stops moving.
//
// "Stops moving" rather than "reaches sent": under modes that lose records it
// never reaches sent, and waiting for equality would hang precisely on the
// configurations the experiment is most interested in.
func (r *Runner) waitForDrain(ctx context.Context, st *store.Store, sent int64) error {
	const (
		quiet = 3 * time.Second
		limit = 2 * time.Minute
	)
	deadline := r.now().Add(limit)
	var last int64
	lastChange := r.now()

	for r.now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		n, err := appliedCount(ctx, st)
		if err != nil {
			return err
		}
		if n != last {
			last, lastChange = n, r.now()
			continue
		}
		if r.now().Sub(lastChange) >= quiet {
			return nil
		}
		if n >= sent {
			return nil
		}
	}
	return fmt.Errorf("still moving after %s", limit)
}

func appliedCount(ctx context.Context, st *store.Store) (int64, error) {
	var n int64
	err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM postings WHERE principal = 'consumer'`).Scan(&n)
	return n, err
}

// measure reads the run's outcome out of the ledger database.
func measure(ctx context.Context, st *store.Store, art *Artefact) error {
	// Applied counts DISTINCT movements that produced an effect. Mode B
	// duplicates by writing a second posting for the same message, so counting
	// postings would make a duplicate look like an extra delivery.
	var applied, postings int64
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM inbox`).Scan(&applied); err != nil {
		return fmt.Errorf("ablation: count inbox: %w", err)
	}
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM postings WHERE principal = 'consumer'`).Scan(&postings); err != nil {
		return fmt.Errorf("ablation: count postings: %w", err)
	}

	// Modes A and B keep no inbox, so distinctness cannot be read from it;
	// there, postings ARE the applied count and duplicates are invisible to the
	// ledger by construction -- which is the property being measured.
	if applied == 0 {
		applied = postings
	}
	art.Applied = applied
	art.Duplicated = maxInt64(0, postings-applied)
	art.Lost = maxInt64(0, art.Sent-applied)

	inv, err := store.GlobalInvariant(ctx, st.Pool)
	if err != nil {
		return fmt.Errorf("ablation: invariant: %w", err)
	}
	art.InvariantHeld = true
	for _, v := range inv {
		if v != 0 {
			art.InvariantHeld = false
		}
	}
	return nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// startLedger launches the ledger as a child process and waits for it to be
// serving.
func (r *Runner) startLedger(ctx context.Context, dsn string, mode consumer.Mode, group, topic string) (func(), error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	// The binary path comes from the operator's own flag, and every argument
	// below is either a constant or a value this runner constructed; nothing
	// here is derived from a broker message or any other untrusted input.
	//nolint:gosec // G204: arguments are operator-supplied and runner-constructed
	cmd := exec.CommandContext(ctx, r.Spec.LedgerBinary,
		"-dsn", dsn,
		"-mode", string(mode),
		"-addr", addr,
		"-brokers", strings.Join(r.Cluster.Seeds(), ","),
		"-group", group,
		"-topic", topic,
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ablation: start ledger: %w", err)
	}
	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}
	if err := waitHealthy(ctx, addr, 30*time.Second); err != nil {
		stop()
		return nil, err
	}
	return stop, nil
}

// provision creates a database for one run and returns a cleanup.
func provision(ctx context.Context, adminDSN, runID string) (string, func(), error) {
	name := "sb_ablate_" + strings.Map(func(rn rune) rune {
		switch {
		case rn >= 'a' && rn <= 'z', rn >= '0' && rn <= '9':
			return rn
		case rn >= 'A' && rn <= 'Z':
			return rn + 32
		default:
			return '_'
		}
	}, runID)

	admin, err := store.Open(ctx, adminDSN)
	if err != nil {
		return "", nil, fmt.Errorf("ablation: open admin database: %w", err)
	}
	defer admin.Close()

	if _, err := admin.Pool.Exec(ctx, `DROP DATABASE IF EXISTS `+quoteIdent(name)); err != nil {
		return "", nil, fmt.Errorf("ablation: drop stale run database: %w", err)
	}
	if _, err := admin.Pool.Exec(ctx, `CREATE DATABASE `+quoteIdent(name)); err != nil {
		return "", nil, fmt.Errorf("ablation: create run database: %w", err)
	}

	dsn, err := withDatabase(adminDSN, name)
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		a, err := store.Open(cleanupCtx, adminDSN)
		if err != nil {
			return
		}
		defer a.Close()
		_, _ = a.Pool.Exec(cleanupCtx,
			`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1`, name)
		_, _ = a.Pool.Exec(cleanupCtx, `DROP DATABASE IF EXISTS `+quoteIdent(name))
	}
	return dsn, cleanup, nil
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func withDatabase(dsn, name string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("ablation: parse dsn: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}

// seedAccounts creates the accounts movements are spread over, deterministically.
func seedAccounts(ctx context.Context, st *store.Store, n int) ([]uuid.UUID, error) {
	ns := uuid.MustParse("6b3f6a1e-3f4a-4a1e-9b2d-2a7c1e0d5f10")
	ids := make([]uuid.UUID, 0, n)
	for i := 0; i < n; i++ {
		id := uuid.NewSHA1(ns, []byte(fmt.Sprintf("ablation/account/%d", i)))
		err := store.InsertAccount(ctx, st.Pool, store.Account{
			ID: id, ProductCode: "CHK-01", Currency: "USD",
			OpenedOn: bizdate.Date(2018, time.June, 1),
		})
		if err != nil && !store.IsUniqueViolation(err, "") {
			return nil, fmt.Errorf("ablation: seed account: %w", err)
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].String() < ids[j].String() })
	return ids, nil
}
