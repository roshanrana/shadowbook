package broker_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"

	"github.com/roshanrana/shadowbook/internal/broker"
)

// These tests run the franz-go clients against kfake, which speaks the real
// Kafka wire protocol over real sockets: produce, fetch, consumer-group join,
// heartbeat, and offset commit/fetch are all exercised as protocol exchanges
// rather than as method calls on a mock.
//
// What that DOES verify: that KafkaProducer and KafkaConsumer drive the
// protocol correctly -- acks, group membership, and above all that a commit is
// durable at the moment Commit returns, which is the property the four
// delivery modes are distinguished by.
//
// What it does NOT verify, and must never be reported as verifying: Finding 2.
// Finding 2 is a measurement of a real three-broker cluster losing replicas
// under load (HLD line 20). kfake is a single in-process broker that cannot be
// killed mid-write, so numbers taken here would describe the harness, not the
// system. See ablation.BrokerFake.

const testTopic = "shadowbook.movements.v1"

func newCluster(t *testing.T) *kfake.Cluster {
	t.Helper()
	c, err := kfake.NewCluster(
		kfake.NumBrokers(1),
		kfake.SeedTopics(1, testTopic),
		// The default minimum is tens of seconds; a test that joins a group
		// should not spend that long doing it.
		kfake.GroupMinSessionTimeout(100*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("kfake: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

func produce(t *testing.T, seeds []string, n int) {
	t.Helper()
	p, err := broker.NewKafkaProducer(broker.KafkaConfig{Seeds: seeds})
	if err != nil {
		t.Fatalf("producer: %v", err)
	}
	defer func() { _ = p.Close() }()

	recs := make([]broker.Record, 0, n)
	for i := 0; i < n; i++ {
		recs = append(recs, broker.Record{
			Topic: testTopic,
			Key:   fmt.Sprintf("acct-%d", i%3),
			Value: []byte(fmt.Sprintf("movement-%d", i)),
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := p.Produce(ctx, recs); err != nil {
		t.Fatalf("produce: %v", err)
	}
}

// pollUntil drains up to want records, giving the group time to join.
type poller interface {
	Poll(ctx context.Context, n int) ([]broker.Record, error)
}

func pollUntil(t *testing.T, c poller, want int) []broker.Record {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var got []broker.Record
	deadline := time.Now().Add(25 * time.Second)
	for len(got) < want && time.Now().Before(deadline) {
		recs, err := c.Poll(ctx, want-len(got))
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
		got = append(got, recs...)
	}
	return got
}

func TestKafkaRoundTrip(t *testing.T) {
	cl := newCluster(t)
	seeds := cl.ListenAddrs()
	produce(t, seeds, 10)

	c, err := broker.NewKafkaConsumer(broker.KafkaConfig{
		Seeds: seeds, Group: "rt", Topics: []string{testTopic},
	})
	if err != nil {
		t.Fatalf("consumer: %v", err)
	}
	defer func() { _ = c.Close() }()

	got := pollUntil(t, c, 10)
	if len(got) != 10 {
		t.Fatalf("polled %d records, want 10", len(got))
	}
	for i, r := range got {
		if r.Topic != testTopic {
			t.Fatalf("record %d topic = %q", i, r.Topic)
		}
		if len(r.Value) == 0 {
			t.Fatalf("record %d has an empty value", i)
		}
	}
}

// A commit must be durable when Commit returns. Mode A commits BEFORE applying
// precisely so that a crash in between loses the effect; if the commit were
// merely queued, mode A would lose nothing and would stop being distinguishable
// from mode B.
func TestKafkaCommitIsDurableOnReturn(t *testing.T) {
	cl := newCluster(t)
	seeds := cl.ListenAddrs()
	produce(t, seeds, 6)

	first, err := broker.NewKafkaConsumer(broker.KafkaConfig{
		Seeds: seeds, Group: "durable", Topics: []string{testTopic},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := pollUntil(t, first, 6)
	if len(got) != 6 {
		t.Fatalf("polled %d, want 6", len(got))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := first.Commit(ctx, got); err != nil {
		t.Fatalf("commit: %v", err)
	}
	_ = first.Close()

	// A fresh member of the SAME group must resume after the committed offset.
	second, err := broker.NewKafkaConsumer(broker.KafkaConfig{
		Seeds: seeds, Group: "durable", Topics: []string{testTopic},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()

	again, err := second.Poll(context.Background(), 100)
	if err != nil {
		t.Fatalf("poll after commit: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("redelivered %d records after a successful commit", len(again))
	}

	// A consumer that had not finished joining the group would ALSO have
	// returned nothing, so the assertion above passes vacuously on its own.
	// Produce fresh records and require exactly those: that can only succeed
	// if this consumer is genuinely a live member reading from the committed
	// offset, which is what the test claims to have shown.
	produce(t, seeds, 3)
	fresh := pollUntil(t, second, 3)
	if len(fresh) != 3 {
		t.Fatalf("second consumer read %d of 3 newly produced records; it was not "+
			"actually consuming, so the no-redelivery result above proved nothing", len(fresh))
	}
}

// The converse, and the reason at-least-once modes see duplicates: records that
// were consumed but NOT committed come back.
func TestKafkaUncommittedRecordsAreRedelivered(t *testing.T) {
	cl := newCluster(t)
	seeds := cl.ListenAddrs()
	produce(t, seeds, 4)

	first, err := broker.NewKafkaConsumer(broker.KafkaConfig{
		Seeds: seeds, Group: "redeliver", Topics: []string{testTopic},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := pollUntil(t, first, 4)
	if len(got) != 4 {
		t.Fatalf("polled %d, want 4", len(got))
	}
	// Deliberately no Commit: this is the crash-before-commit case.
	_ = first.Close()

	second, err := broker.NewKafkaConsumer(broker.KafkaConfig{
		Seeds: seeds, Group: "redeliver", Topics: []string{testTopic},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()

	again := pollUntil(t, second, 4)
	if len(again) != 4 {
		t.Fatalf("redelivered %d records, want all 4 back after no commit", len(again))
	}
}

func TestKafkaConfigValidation(t *testing.T) {
	if _, err := broker.NewKafkaProducer(broker.KafkaConfig{}); err == nil {
		t.Fatal("producer accepted an empty seed list")
	}
	if _, err := broker.NewKafkaConsumer(broker.KafkaConfig{Seeds: []string{"x:1"}}); err == nil {
		t.Fatal("consumer accepted an empty group id")
	}
	if _, err := broker.NewKafkaConsumer(broker.KafkaConfig{
		Seeds: []string{"x:1"}, Group: "g",
	}); err == nil {
		t.Fatal("consumer accepted an empty topic list")
	}
}

// Poll must return empty on a quiet topic rather than parking until traffic
// arrives, or shutdown would depend on traffic that is not coming.
func TestKafkaPollReturnsEmptyWhenQuiet(t *testing.T) {
	cl := newCluster(t)
	c, err := broker.NewKafkaConsumer(broker.KafkaConfig{
		Seeds: cl.ListenAddrs(), Group: "quiet", Topics: []string{testTopic},
		PollTimeout: 150 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	// First poll may block on the group join; the one after it must be quick.
	_, _ = c.Poll(context.Background(), 10)

	start := time.Now()
	recs, err := c.Poll(context.Background(), 10)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if len(recs) != 0 {
		t.Fatalf("got %d records from an empty topic", len(recs))
	}
	if elapsed > 3*time.Second {
		t.Fatalf("poll on a quiet topic took %s; it should bound itself", elapsed)
	}
}

// Configuration D CANNOT be verified against kfake, and this test records that
// rather than pretending otherwise.
//
// kfake implements the transaction request types (AddPartitionsToTxn, EndTxn,
// TxnOffsetCommit) but not the entry point: its handleInitProducerID returns
// UNKNOWN_SERVER_ERROR for any request carrying a transactional id, with a
// literal "TODO: Transactional IDs" above it. So the session cannot begin, and
// no amount of harness work here would change that.
//
// The consequence is stated plainly in the ship report: the transactional
// consumer is IMPLEMENTED but UNVERIFIED until it runs against a real cluster.
// A skipped test that says why is worth more than a passing test that exercises
// a path the broker never took.
func TestKafkaTransactionalConsumerNeedsARealBroker(t *testing.T) {
	cl := newCluster(t)
	seeds := cl.ListenAddrs()
	produce(t, seeds, 4)

	c, err := broker.NewKafkaTransactionalConsumer(broker.KafkaConfig{
		Seeds: seeds, Group: "txn-probe", Topics: []string{testTopic},
	})
	if err != nil {
		t.Fatalf("constructing the session should succeed even so: %v", err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = c.Poll(ctx, 4)
	if err == nil {
		t.Fatal("kfake accepted a transactional producer id -- it has gained " +
			"transaction support, so this test should become a real assertion " +
			"that D commits offsets transactionally")
	}
	if !broker.IsTransient(err) {
		t.Fatalf("a broker that refuses to start a transaction should surface as "+
			"transient, not fatal: %v", err)
	}
	t.Skip("kfake does not implement transactional producer ids; configuration D " +
		"is verifiable only against a real cluster")
}

func TestKafkaTransactionalConsumerValidation(t *testing.T) {
	if _, err := broker.NewKafkaTransactionalConsumer(broker.KafkaConfig{}); err == nil {
		t.Fatal("accepted an empty seed list")
	}
	if _, err := broker.NewKafkaTransactionalConsumer(broker.KafkaConfig{
		Seeds: []string{"x:1"},
	}); err == nil {
		t.Fatal("accepted an empty group id")
	}
}
