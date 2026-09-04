package broker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

// DefaultPollTimeout bounds how long Poll waits for records before returning
// empty.
//
// The consumer loop treats "no records" as a cue to sleep briefly and poll
// again, and it checks for cancellation between iterations. An unbounded poll
// would park inside franz-go and make shutdown depend on traffic arriving --
// during the quiet tail of an ablation run, which is exactly when the runner
// wants to stop, no traffic is arriving.
const DefaultPollTimeout = 250 * time.Millisecond

// KafkaConfig configures the real broker clients.
type KafkaConfig struct {
	Seeds []string
	// Group is the consumer group id. Every configuration under ablation uses
	// a DISTINCT group, so a run never inherits another run's committed
	// offsets -- inherited offsets would look like message loss.
	Group       string
	Topics      []string
	PollTimeout time.Duration
	// ClientID appears in broker logs; naming the configuration makes a
	// captured broker log readable after the fact.
	ClientID string
}

func (c KafkaConfig) validate() error {
	if len(c.Seeds) == 0 {
		return errors.New("broker: no seed brokers configured")
	}
	return nil
}

// EnsureTopic creates a topic, treating "already exists" as success.
//
// The ablation gives each run its own topic and therefore has to create it.
// Relying on the broker's auto-creation instead would make the experiment
// depend on a cluster-wide setting that is off by default in production
// Redpanda -- the run would simply produce nothing and report total loss,
// which looks exactly like the result the experiment is trying to measure.
func EnsureTopic(ctx context.Context, seeds []string, topic string, partitions int32, replicas int16) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers(seeds...), kgo.ClientID("shadowbook-admin"))
	if err != nil {
		return fmt.Errorf("broker: admin client: %w", err)
	}
	defer cl.Close()

	adm := kadm.NewClient(cl)
	resp, err := adm.CreateTopic(ctx, partitions, replicas, nil, topic)
	if err != nil {
		if errors.Is(err, kerr.TopicAlreadyExists) {
			return nil
		}
		return fmt.Errorf("broker: create topic %s: %w", topic, err)
	}
	if resp.Err != nil && !errors.Is(resp.Err, kerr.TopicAlreadyExists) {
		return fmt.Errorf("broker: create topic %s: %w", topic, resp.Err)
	}
	return nil
}

// GroupLag reports a consumer group's total lag without joining the group.
//
// Joining would be the obvious way to ask and is exactly wrong: a new member
// triggers a rebalance and takes partitions away from the consumer being
// measured, so the act of observing would change the thing observed.
func GroupLag(ctx context.Context, seeds []string, group string) (int64, error) {
	cl, err := kgo.NewClient(kgo.SeedBrokers(seeds...), kgo.ClientID("shadowbook-lag"))
	if err != nil {
		return 0, fmt.Errorf("broker: lag client: %w", err)
	}
	defer cl.Close()

	lags, err := kadm.NewClient(cl).Lag(ctx, group)
	if err != nil {
		return 0, fmt.Errorf("broker: group lag: %w", err)
	}
	var total int64
	for _, described := range lags {
		if described.FetchErr != nil || described.DescribeErr != nil {
			continue
		}
		for _, partitions := range described.Lag {
			for _, l := range partitions {
				if l.Lag > 0 {
					total += l.Lag
				}
			}
		}
	}
	return total, nil
}

// KafkaProducer publishes with acks=all.
//
// NFR-9 requires that Produce not return until every record is durably
// acknowledged, because the outbox relay marks rows sent the instant it
// returns. A fire-and-forget produce would make the outbox claim delivery for
// records the cluster never took, which is indistinguishable -- from the
// ledger's side -- from a broker that accepted and lost them. That distinction
// is what Finding 2 measures, so the producer must not blur it.
type KafkaProducer struct {
	cl *kgo.Client
}

// NewKafkaProducer dials the cluster.
func NewKafkaProducer(cfg KafkaConfig) (*KafkaProducer, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	id := cfg.ClientID
	if id == "" {
		id = "shadowbook-relay"
	}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Seeds...),
		kgo.ClientID(id),
		// acks=all plus the idempotent producer: retries cannot silently
		// duplicate a record, so a duplicate observed downstream is a property
		// of the DELIVERY MODE under test and not an artefact of the client.
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchMaxBytes(1<<20),
		// Records for one account must keep their order, so partition by key.
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)),
	)
	if err != nil {
		return nil, fmt.Errorf("broker: kafka producer: %w", err)
	}
	return &KafkaProducer{cl: cl}, nil
}

// Produce writes every record and waits for all acknowledgements.
func (p *KafkaProducer) Produce(ctx context.Context, records []Record) error {
	if len(records) == 0 {
		return nil
	}
	rs := make([]*kgo.Record, 0, len(records))
	for _, r := range records {
		rs = append(rs, &kgo.Record{
			Topic: r.Topic,
			Key:   []byte(r.Key),
			Value: r.Value,
		})
	}
	// ProduceSync returns only once every record has been acked or has failed
	// permanently; FirstErr collapses the results.
	if err := p.cl.ProduceSync(ctx, rs...).FirstErr(); err != nil {
		return fmt.Errorf("broker: produce %d records: %w", len(rs), err)
	}
	return nil
}

// Close flushes and shuts the client down.
func (p *KafkaProducer) Close() error {
	p.cl.Close()
	return nil
}

// KafkaConsumer reads from a consumer group with autocommit DISABLED.
//
// Autocommit is the single most important thing to turn off here. The four
// delivery modes differ only in when Commit is called relative to applying an
// effect; a background goroutine committing on a timer would overwrite that
// ordering and every configuration would measure the same thing.
type KafkaConsumer struct {
	cl      *kgo.Client
	adm     *kadm.Client
	group   string
	timeout time.Duration

	// dataLoss counts partitions the cluster reset out from under this
	// consumer. Exposed so a run artefact can report cluster-caused loss as a
	// number rather than leaving it to be inferred from a shortfall.
	dataLoss atomic.Int64
}

// DataLossEvents is how many times the cluster dropped records this consumer
// had not read.
func (c *KafkaConsumer) DataLossEvents() int64 { return c.dataLoss.Load() }

// isDataLoss reports whether the cluster lost records the client had not read.
func isDataLoss(err error) bool {
	var dl *kgo.ErrDataLoss
	return errors.As(err, &dl)
}

// isRetriable reports whether franz-go will recover on its own.
//
// Kafka protocol errors carry their own retriability, and connection-level
// failures are retriable by definition: a broker that went away is the very
// condition being tested, not a reason to stop.
func isRetriable(err error) bool {
	var ke *kerr.Error
	if errors.As(err, &ke) {
		return ke.Retriable
	}
	return !errors.Is(err, kgo.ErrClientClosed) && isConnErr(err)
}

func isConnErr(err error) bool {
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}

// NewKafkaConsumer joins the group.
func NewKafkaConsumer(cfg KafkaConfig) (*KafkaConsumer, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.Group == "" {
		return nil, errors.New("broker: consumer group id is required")
	}
	if len(cfg.Topics) == 0 {
		return nil, errors.New("broker: no topics to consume")
	}
	id := cfg.ClientID
	if id == "" {
		id = "shadowbook-consumer"
	}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(cfg.Seeds...),
		kgo.ClientID(id),
		kgo.ConsumerGroup(cfg.Group),
		kgo.ConsumeTopics(cfg.Topics...),
		// Start at the beginning: a run measures every record the relay
		// produced, and defaulting to the end would silently drop whatever was
		// produced before the consumer finished joining.
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return nil, fmt.Errorf("broker: kafka consumer: %w", err)
	}
	timeout := cfg.PollTimeout
	if timeout <= 0 {
		timeout = DefaultPollTimeout
	}
	return &KafkaConsumer{cl: cl, adm: kadm.NewClient(cl), group: cfg.Group, timeout: timeout}, nil
}

// Poll fetches up to max records, returning empty rather than blocking
// indefinitely when the topic is quiet.
func (c *KafkaConsumer) Poll(ctx context.Context, maxRecords int) ([]Record, error) {
	pollCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	fetches := c.cl.PollRecords(pollCtx, maxRecords)
	// A deadline reached with nothing to read is the normal quiet case, not an
	// error; only the CALLER's cancellation should surface as one.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Not every fetch error is fatal, and treating them all as fatal was a real
	// defect: under a broker kill the ledger exited instead of riding it out.
	//
	// Three kinds, and only the third should stop anything:
	//
	//  - the poll deadline or the caller's cancellation: the normal quiet case;
	//  - a retriable broker error or data loss: the cluster changed under us.
	//    franz-go has already refreshed metadata and reset the partition, and
	//    the right response is to note it and keep consuming. Exiting here is
	//    the worst possible reaction for a ledger whose stated job is to
	//    survive broker loss -- and it silently converts a recoverable backlog
	//    into permanent loss, because a dead consumer applies nothing;
	//  - anything else, which is a real fault.
	//
	// Data loss is counted rather than swallowed. It means the CLUSTER dropped
	// records this consumer had not yet read, which is exactly the event the
	// ablation is measuring, so it must be visible in the artefact rather than
	// inferred from a gap in the totals.
	for _, e := range fetches.Errors() {
		switch {
		case errors.Is(e.Err, context.DeadlineExceeded), errors.Is(e.Err, context.Canceled):
			continue
		case isDataLoss(e.Err):
			c.dataLoss.Add(1)
			continue
		case isRetriable(e.Err):
			continue
		default:
			return nil, fmt.Errorf("broker: fetch %s[%d]: %w", e.Topic, e.Partition, e.Err)
		}
	}

	out := make([]Record, 0, fetches.NumRecords())
	fetches.EachRecord(func(r *kgo.Record) {
		out = append(out, Record{
			Topic:     r.Topic,
			Key:       string(r.Key),
			Value:     r.Value,
			Partition: r.Partition,
			Offset:    r.Offset,
		})
	})
	return out, nil
}

// Commit commits the offsets for the supplied records, and does not return
// until the broker has confirmed them.
//
// Synchronous on purpose: mode A's defining property is that the offset is
// durable BEFORE the effect is attempted. An asynchronous commit would leave
// mode A indistinguishable from mode B under a kill, which is the comparison
// the whole ablation exists to make.
func (c *KafkaConsumer) Commit(ctx context.Context, records []Record) error {
	if len(records) == 0 {
		return nil
	}
	rs := make([]*kgo.Record, 0, len(records))
	for _, r := range records {
		rs = append(rs, &kgo.Record{Topic: r.Topic, Partition: r.Partition, Offset: r.Offset})
	}
	if err := c.cl.CommitRecords(ctx, rs...); err != nil {
		return fmt.Errorf("broker: commit %d offsets: %w", len(rs), err)
	}
	return nil
}

// Lag is the committed-offset lag for this group across its partitions.
func (c *KafkaConsumer) Lag(ctx context.Context) (int64, error) {
	lags, err := c.adm.Lag(ctx, c.group)
	if err != nil {
		return 0, fmt.Errorf("broker: lag: %w", err)
	}
	var total int64
	for _, described := range lags {
		if described.FetchErr != nil || described.DescribeErr != nil {
			continue
		}
		for _, partitions := range described.Lag {
			for _, l := range partitions {
				if l.Lag > 0 {
					total += l.Lag
				}
			}
		}
	}
	return total, nil
}

// Close leaves the group.
func (c *KafkaConsumer) Close() error {
	c.cl.Close()
	return nil
}

var (
	_ Producer = (*KafkaProducer)(nil)
	_ Consumer = (*KafkaConsumer)(nil)
)
