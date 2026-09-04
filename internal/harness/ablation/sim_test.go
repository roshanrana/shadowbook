package ablation_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/roshanrana/shadowbook/internal/broker"
	"github.com/roshanrana/shadowbook/internal/harness/ablation"
)

// These assert the properties the simulated cluster is CLAIMED to have. The
// claims are what the Finding 2 (simulated) section rests on, so each one is
// checked rather than described: a broker really stops listening when killed,
// the cluster keeps serving without it, the log survives, and the broker comes
// back on the same address.

func dialable(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func simTopic(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("sim.%s.v1", t.Name())
}

func TestSimClusterKillsAndRestartsARealListener(t *testing.T) {
	sim, err := ablation.NewSimCluster(3)
	if err != nil {
		t.Fatalf("sim cluster: %v", err)
	}
	defer sim.Close()

	if got := len(sim.Seeds()); got != 3 {
		t.Fatalf("cluster has %d brokers, want 3", got)
	}
	before := append([]string(nil), sim.Seeds()...)

	// Find the address the chaos schedule's "redpanda-2" refers to, by
	// elimination: it must be the one that stops listening.
	ctx := context.Background()
	if err := sim.Kill(ctx, "redpanda-2"); err != nil {
		t.Fatalf("kill: %v", err)
	}

	var killed string
	for _, addr := range before {
		if !dialable(addr) {
			killed = addr
		}
	}
	if killed == "" {
		t.Fatal("killing redpanda-2 closed no listener; the kill did nothing")
	}
	if len(sim.Seeds()) != 2 {
		t.Fatalf("cluster reports %d brokers after a kill, want 2", len(sim.Seeds()))
	}

	if err := sim.Start(ctx, "redpanda-2"); err != nil {
		t.Fatalf("start: %v", err)
	}
	// Same address, because a client holding stale metadata has to be able to
	// find it again -- that is the recovery path being measured.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !dialable(killed) {
		time.Sleep(50 * time.Millisecond)
	}
	if !dialable(killed) {
		t.Fatalf("broker did not come back on %s", killed)
	}
	if len(sim.Seeds()) != 3 {
		t.Fatalf("cluster reports %d brokers after a restart, want 3", len(sim.Seeds()))
	}
}

// The property that makes this a usable stand-in for a quorum-preserving kill:
// records produced before the kill are still readable after it.
func TestSimClusterKeepsTheLogAcrossAKill(t *testing.T) {
	sim, err := ablation.NewSimCluster(3)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	topic := simTopic(t)
	if err := broker.EnsureTopic(ctx, sim.Seeds(), topic, 6, 1); err != nil {
		t.Fatalf("create topic: %v", err)
	}

	prod, err := broker.NewKafkaProducer(broker.KafkaConfig{Seeds: sim.Seeds()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = prod.Close() }()

	recs := make([]broker.Record, 0, 30)
	for i := 0; i < 30; i++ {
		recs = append(recs, broker.Record{
			Topic: topic, Key: fmt.Sprintf("k%d", i%5), Value: []byte(fmt.Sprintf("v%d", i)),
		})
	}
	if err := prod.Produce(ctx, recs); err != nil {
		t.Fatalf("produce before kill: %v", err)
	}

	if err := sim.Kill(ctx, "redpanda-1"); err != nil {
		t.Fatalf("kill: %v", err)
	}

	// Everything produced before the kill must still be readable, or the
	// simulation would be modelling data loss rather than failover -- a
	// different experiment from the one the chaos schedule describes.
	cons, err := broker.NewKafkaConsumer(broker.KafkaConfig{
		Seeds: sim.Seeds(), Group: "sim-survive", Topics: []string{topic},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cons.Close() }()

	var got int
	deadline := time.Now().Add(30 * time.Second)
	for got < 30 && time.Now().Before(deadline) {
		batch, err := cons.Poll(ctx, 100)
		if err != nil {
			t.Fatalf("poll after kill: %v", err)
		}
		got += len(batch)
	}
	if got != 30 {
		t.Fatalf("read %d of 30 records after a broker was killed; the log did not survive", got)
	}
}

func TestSimClusterRejectsBadInput(t *testing.T) {
	if _, err := ablation.NewSimCluster(1); err == nil {
		t.Fatal("accepted a one-broker cluster; nothing could then be killed")
	}
	sim, err := ablation.NewSimCluster(2)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()

	ctx := context.Background()
	if err := sim.Kill(ctx, "redpanda-9"); err == nil {
		t.Fatal("killed a broker that does not exist")
	}
	if err := sim.Kill(ctx, "redpanda-1"); err != nil {
		t.Fatal(err)
	}
	if err := sim.Kill(ctx, "redpanda-1"); err == nil {
		t.Fatal("killed the same broker twice; the schedule would be silently wrong")
	}
}

func TestSimArtefactsAreSimulatedNotReal(t *testing.T) {
	sim, err := ablation.NewSimCluster(2)
	if err != nil {
		t.Fatal(err)
	}
	defer sim.Close()

	a := ablation.Artefact{RunID: "r", BrokerVersion: sim.Version()}
	if a.Kind() != ablation.KindSimulated {
		t.Fatalf("kind = %s, want %s", a.Kind(), ablation.KindSimulated)
	}
	// It measures something, so it is not refused outright the way a plumbing
	// run is -- but it must never be labelled real.
	if !a.IsMeasurement() {
		t.Fatal("a simulated run should count as a measurement")
	}
	real0 := ablation.Artefact{RunID: "x", BrokerVersion: "redpanda v24.3.6"}
	if _, err := ablation.KindOf([]ablation.Artefact{a, real0}); err == nil {
		t.Fatal("a table mixing simulated and real runs was allowed")
	}
}
