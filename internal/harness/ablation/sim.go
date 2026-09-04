package ablation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/roshanrana/shadowbook/internal/harness/chaos"
)

// BrokerSim is the BrokerVersion prefix for a run against the simulated
// multi-broker cluster.
//
// Deliberately distinct from BrokerFake. They are not the same claim:
//
//   - BrokerFake means a single in-process broker that cannot fail. Such a run
//     checks that the orchestration works and measures nothing. Table refuses it.
//   - BrokerSim means several brokers on real sockets, killed and restarted on
//     the chaos schedule, with the log surviving the kill. That IS an ablation
//     of delivery semantics under broker failover, and its numbers mean
//     something -- just not the same thing as Redpanda's.
//
// Table accepts BrokerSim runs and reports their kind, so the renderer can put
// them under their own heading. What it must never do is let them appear as
// the real thing.
const BrokerSim = "sim:"

// SimCluster is a multi-broker Kafka cluster running in this process.
//
// It is a real cluster in the ways the ablation depends on: each broker has its
// own TCP listener, clients speak the Kafka protocol to it, and killing one
// closes its listener, bumps the partition leader epoch and moves leadership to
// a survivor -- so producers and consumers must notice, refetch metadata and
// fail over, which is the code path under test.
//
// It is NOT a real cluster in the ways Redpanda would be: there is no
// replication, no ISR to shrink, no unclean leader election, no disk. The log
// is cluster-wide, so a killed broker cannot lose data, which is the same
// outcome as the designed chaos schedule (quorum preserved throughout) but
// achieved by not having the problem rather than by solving it.
type SimCluster struct {
	cluster *kfake.Cluster
	ver     string

	mu    sync.Mutex
	ports map[string]int   // container name -> port, so a restart returns to it
	nodes map[string]int32 // container name -> node id
	down  map[string]bool
}

// NewSimCluster starts n brokers.
func NewSimCluster(n int) (*SimCluster, error) {
	if n < 2 {
		return nil, fmt.Errorf("ablation: a simulated cluster needs at least 2 brokers, got %d", n)
	}
	cl, err := kfake.NewCluster(
		kfake.NumBrokers(n),
		kfake.AllowAutoTopicCreation(),
		kfake.DefaultNumPartitions(6),
		kfake.GroupMinSessionTimeout(100*time.Millisecond),
	)
	if err != nil {
		return nil, fmt.Errorf("ablation: start simulated cluster: %w", err)
	}

	s := &SimCluster{
		cluster: cl,
		ver:     fmt.Sprintf("%skfake-%d-broker", BrokerSim, n),
		ports:   map[string]int{},
		nodes:   map[string]int32{},
		down:    map[string]bool{},
	}

	// Map the chaos schedule's container names onto node ids, asking the
	// cluster over the protocol rather than inferring from ListenAddrs.
	//
	// Inferring would be wrong twice over. ListenAddrs returns addresses with
	// no node ids attached, and RemoveNode swaps the removed broker to the end
	// of the internal slice -- so the order is not even stable across a kill.
	// Killing "redpanda-1" after one restart could then take down a different
	// broker than the name says, and the run would still look fine.
	if err := s.mapBrokers(); err != nil {
		cl.Close()
		return nil, err
	}
	return s, nil
}

// mapBrokers asks the cluster for its (node id, host, port) list and names each
// broker the way the chaos schedule does.
func (s *SimCluster) mapBrokers() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	admin, err := kgo.NewClient(kgo.SeedBrokers(s.cluster.ListenAddrs()...), kgo.ClientID("sim-admin"))
	if err != nil {
		return fmt.Errorf("ablation: simulated cluster admin: %w", err)
	}
	defer admin.Close()

	brokers, err := kadm.NewClient(admin).ListBrokers(ctx)
	if err != nil {
		return fmt.Errorf("ablation: list simulated brokers: %w", err)
	}
	// kadm sorts by node id, so redpanda-1 is the lowest node id on every run.
	for i, b := range brokers {
		name := fmt.Sprintf("redpanda-%d", i+1)
		s.ports[name] = int(b.Port)
		s.nodes[name] = b.NodeID
	}
	if len(s.nodes) == 0 {
		return errors.New("ablation: simulated cluster reported no brokers")
	}
	return nil
}

func (s *SimCluster) Seeds() []string { return s.cluster.ListenAddrs() }

// Replicas is 1: kfake keeps one cluster-wide log and does not replicate, so
// asking for more would be a number with nothing behind it.
func (s *SimCluster) Replicas() int16 { return 1 }

func (s *SimCluster) Version() string { return s.ver }

// Docker returns this cluster as its own control surface: a "container" here is
// a broker, and killing it closes its listener.
func (s *SimCluster) Docker() chaos.Docker { return s }

// Close shuts the whole cluster down.
func (s *SimCluster) Close() { s.cluster.Close() }

// Kill removes a broker from the cluster, closing its listener.
func (s *SimCluster) Kill(_ context.Context, container string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[container]
	if !ok {
		return fmt.Errorf("ablation: no simulated broker named %q", container)
	}
	if s.down[container] {
		return fmt.Errorf("ablation: %s is already down", container)
	}
	if err := s.cluster.RemoveNode(node); err != nil {
		return fmt.Errorf("ablation: kill %s: %w", container, err)
	}

	// Move the group coordinator too, because otherwise this kill does not
	// exercise the thing being measured.
	//
	// The delivery modes differ in what happens across a REBALANCE: mode A has
	// committed offsets for records it had not yet applied and loses them,
	// mode B is redelivered records it already applied and duplicates them.
	// Nothing forces a rebalance unless the group's coordinator moves. In a
	// real cluster, killing the broker that hosts the coordinator does exactly
	// that; removing a kfake node on its own does not, so without this the
	// three configurations produce near-identical numbers and the ablation
	// silently measures nothing. That is what the first simulated sweep did.
	s.cluster.RehashCoordinators()

	s.down[container] = true
	return nil
}

// Start brings a killed broker back on the SAME port.
//
// The port matters: a broker that came back somewhere else would be a new
// broker as far as any client holding stale metadata is concerned, and the
// recovery path being measured is the one where the address comes back.
func (s *SimCluster) Start(_ context.Context, container string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.nodes[container]
	if !ok {
		return fmt.Errorf("ablation: no simulated broker named %q", container)
	}
	if !s.down[container] {
		return nil // already running; restarting a live broker is a no-op
	}
	if _, _, err := s.cluster.AddNode(node, s.ports[container]); err != nil {
		return fmt.Errorf("ablation: start %s: %w", container, err)
	}
	s.down[container] = false
	return nil
}

var (
	_ Cluster      = (*SimCluster)(nil)
	_ chaos.Docker = (*SimCluster)(nil)
)
