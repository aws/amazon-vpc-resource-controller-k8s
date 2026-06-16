// Copyright Amazon.com Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//     http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

// This file holds the concurrency-focused tests and benchmark for the node
// manager's AddNode/UpdateNode lock behavior. They are intentionally added
// against the original whole-function-locked implementation first: the
// barrier test (TestAddNode_LockFreePathRunsInParallel) and the benchmark
// document the inflow bottleneck, and the follow-up fix is what turns the
// barrier test green and the benchmark ~10x faster.
package manager

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	rcV1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/api"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/config"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/k8s"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node"
	asyncWorker "github.com/aws/amazon-vpc-resource-controller-k8s/pkg/worker"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// Benchmark tuning knobs. These are intentionally fixed (not randomized) so the
// before/after comparison is deterministic.
const (
	// benchNumNodes is how many distinct nodes are added per benchmark op.
	benchNumNodes = 120
	// benchConcurrency matches the default --max-node-reconcile so the benchmark
	// models the real reconciler goroutine fan-in onto the manager lock.
	benchConcurrency = 10
	// benchCallLatency is the simulated per-call latency for each K8s API call
	// that AddNode performs while (currently) holding the manager lock. Keeping
	// this non-zero is what makes the lock-serialization cost observable.
	benchCallLatency = 3 * time.Millisecond
)

// benchK8s is a hand-written fake K8sWrapper that injects a fixed latency into
// the calls AddNode makes. We deliberately avoid gomock here: gomock's Controller
// serializes every mocked call under a single global mutex, which would mask the
// exact manager-lock contention this benchmark is trying to measure.
//
// The embedded k8s.K8sWrapper interface is nil; only the methods AddNode actually
// calls are overridden. Any other method would panic if called (it won't be).
type benchK8s struct {
	k8s.K8sWrapper
	delay time.Duration
}

func (f *benchK8s) GetNode(nodeName string) (*v1.Node, error) {
	time.Sleep(f.delay)
	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				config.NodeLabelOS:           config.OSLinux,
				config.HasTrunkAttachedLabel: "true",
			},
		},
		Spec: v1.NodeSpec{ProviderID: providerId},
	}, nil
}

func (f *benchK8s) GetCNINode(types.NamespacedName) (*rcV1alpha1.CNINode, error) {
	time.Sleep(f.delay)
	// Returning an existing (empty) CNINode models the leader-transition case
	// where CNINodes already exist, so CreateCNINode is never called. We
	// therefore do not implement CreateCNINode here (the embedded nil interface
	// satisfies the type), which also keeps this fake decoupled from that
	// method's signature.
	return &rcV1alpha1.CNINode{}, nil
}

// benchWorker is a no-op Worker. SubmitJob must do no real work (and take no
// shared lock) so that it does not become an artificial bottleneck.
type benchWorker struct {
	asyncWorker.Worker
}

func (w *benchWorker) SubmitJob(interface{}) {}

func newBenchManager(delay time.Duration) *manager {
	return &manager{
		Log:       logr.Discard(),
		dataStore: make(map[string]node.Node),
		wrapper: api.Wrapper{
			K8sAPI: &benchK8s{delay: delay},
			EC2API: nil,
		},
		worker:      &benchWorker{},
		clusterName: mockClusterName,
	}
}

// BenchmarkAddNode_Concurrent measures the wall-clock time for benchConcurrency
// goroutines to add benchNumNodes distinct nodes through the manager. It calls
// only the public AddNode, so the same benchmark runs unchanged against both the
// current (whole-function-locked) implementation and the refactored one, making
// it a fair before/after baseline.
//
// Run with:
//
//	go test -bench BenchmarkAddNode_Concurrent -benchtime 1x -count 10 ./pkg/node/manager/...
func BenchmarkAddNode_Concurrent(b *testing.B) {
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		m := newBenchManager(benchCallLatency)
		names := make(chan string, benchNumNodes)
		for j := 0; j < benchNumNodes; j++ {
			names <- fmt.Sprintf("bench-node-%d", j)
		}
		close(names)

		var wg sync.WaitGroup
		b.StartTimer()
		for w := 0; w < benchConcurrency; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for name := range names {
					if err := m.AddNode(name); err != nil {
						b.Errorf("AddNode(%s) returned error: %v", name, err)
					}
				}
			}()
		}
		wg.Wait()

		b.StopTimer()
		if len(m.dataStore) != benchNumNodes {
			b.Fatalf("expected %d nodes in dataStore, got %d", benchNumNodes, len(m.dataStore))
		}
		b.StartTimer()
	}
}

// countingWorker is a Worker that atomically counts SubmitJob calls. It takes no
// shared lock so it does not perturb concurrency behavior under the race detector.
type countingWorker struct {
	asyncWorker.Worker
	submitted int32
}

func (w *countingWorker) SubmitJob(interface{}) { atomic.AddInt32(&w.submitted, 1) }

func (w *countingWorker) count() int32 { return atomic.LoadInt32(&w.submitted) }

func newConcurrencyManager(worker asyncWorker.Worker, existing map[string]node.Node) *manager {
	if existing == nil {
		existing = make(map[string]node.Node)
	}
	return &manager{
		Log:       logr.Discard(),
		dataStore: existing,
		wrapper: api.Wrapper{
			K8sAPI: &benchK8s{}, // zero latency; returns a managed (trunk) node
			EC2API: nil,
		},
		worker:      worker,
		clusterName: mockClusterName,
	}
}

// TestAddNode_ConcurrentSameNode_SingleJob asserts that when many goroutines race
// to add the SAME node, the in-lock double-check lets exactly one win: only one
// async job is submitted and the node appears once in the datastore.
//
// Run with -race to also verify there is no concurrent map access.
func TestAddNode_ConcurrentSameNode_SingleJob(t *testing.T) {
	const concurrency = 50
	worker := &countingWorker{}
	m := newConcurrencyManager(worker, nil)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines at once to maximize contention
			if err := m.AddNode("race-node"); err != nil {
				t.Errorf("AddNode returned error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	assert.Equal(t, int32(1), worker.count(), "exactly one job should be submitted for a single node")
	assert.Len(t, m.dataStore, 1, "node should appear exactly once in the datastore")
	assert.Contains(t, m.dataStore, "race-node")
}

// TestAddNode_ConcurrentDistinctNodes_AllAdded asserts that concurrently adding
// many distinct nodes results in every node being added exactly once.
func TestAddNode_ConcurrentDistinctNodes_AllAdded(t *testing.T) {
	const (
		numNodes    = 300
		concurrency = 10
	)
	worker := &countingWorker{}
	m := newConcurrencyManager(worker, nil)

	names := make(chan string, numNodes)
	for j := 0; j < numNodes; j++ {
		names <- fmt.Sprintf("node-%d", j)
	}
	close(names)

	var wg sync.WaitGroup
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range names {
				if err := m.AddNode(name); err != nil {
					t.Errorf("AddNode(%s) returned error: %v", name, err)
				}
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(numNodes), worker.count(), "each distinct node should submit exactly one job")
	assert.Len(t, m.dataStore, numNodes)
}

// TestNode_ConcurrentAddUpdateDelete_NoRace hammers Add/Update/Delete on a small
// set of node names from many goroutines. Its purpose is to exercise the
// compare-and-swap path in UpdateNode and the map mutations under the race
// detector; it asserts the manager does not panic and the datastore stays
// internally consistent.
func TestNode_ConcurrentAddUpdateDelete_NoRace(t *testing.T) {
	const (
		concurrency   = 30
		opsPerRoutine = 100
		distinctNodes = 5
	)
	worker := &countingWorker{}
	m := newConcurrencyManager(worker, nil)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for op := 0; op < opsPerRoutine; op++ {
				name := fmt.Sprintf("node-%d", (seed+op)%distinctNodes)
				switch op % 3 {
				case 0:
					_ = m.AddNode(name)
				case 1:
					_ = m.UpdateNode(name)
				case 2:
					_ = m.DeleteNode(name)
				}
			}
		}(i)
	}
	wg.Wait()

	// No strict count assertion (operations interleave nondeterministically); the
	// real check is the race detector plus the absence of a panic. Sanity-check
	// that the datastore never exceeds the number of distinct node names.
	assert.LessOrEqual(t, len(m.dataStore), distinctNodes)
}

// gateK8s is a fake K8sWrapper whose GetNode blocks at a barrier until `want`
// calls are simultaneously in-flight, then releases them together. It records the
// maximum number of concurrently in-flight GetNode calls observed.
//
// This is the mechanism that turns the qualitative "serial -> parallel" change
// into a deterministic pass/fail assertion: with a whole-function lock, only one
// goroutine can ever be inside AddNode (hence inside GetNode) at a time, so the
// barrier never fills and the test times out.
type gateK8s struct {
	k8s.K8sWrapper // embedded interface (nil); only GetNode/GetCNINode are real

	want        int
	mu          sync.Mutex
	inFlight    int
	maxInFlight int
	arrived     chan struct{} // closed once `want` calls are concurrently in-flight
	release     chan struct{} // closed by the test to let blocked calls proceed
	arriveOnce  sync.Once
}

func (g *gateK8s) GetNode(nodeName string) (*v1.Node, error) {
	g.mu.Lock()
	g.inFlight++
	if g.inFlight > g.maxInFlight {
		g.maxInFlight = g.inFlight
	}
	reached := g.inFlight >= g.want
	g.mu.Unlock()

	if reached {
		g.arriveOnce.Do(func() { close(g.arrived) })
	}
	<-g.release // block until the test releases everyone

	g.mu.Lock()
	g.inFlight--
	g.mu.Unlock()

	return &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				config.NodeLabelOS:           config.OSLinux,
				config.HasTrunkAttachedLabel: "true",
			},
		},
		Spec: v1.NodeSpec{ProviderID: providerId},
	}, nil
}

func (g *gateK8s) GetCNINode(types.NamespacedName) (*rcV1alpha1.CNINode, error) {
	return &rcV1alpha1.CNINode{}, nil
}

// TestAddNode_LockFreePathRunsInParallel asserts that AddNode's K8s work runs
// outside the manager lock: it requires all `concurrency` GetNode calls to be
// in-flight simultaneously. On the previous whole-function-locked implementation
// this is impossible and the test fails via timeout, making it a regression guard
// against re-widening the critical section.
func TestAddNode_LockFreePathRunsInParallel(t *testing.T) {
	const concurrency = 10
	g := &gateK8s{
		want:    concurrency,
		arrived: make(chan struct{}),
		release: make(chan struct{}),
	}
	m := &manager{
		Log:       logr.Discard(),
		dataStore: make(map[string]node.Node),
		wrapper: api.Wrapper{
			K8sAPI: g,
			EC2API: nil,
		},
		worker:      &countingWorker{},
		clusterName: mockClusterName,
	}

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := m.AddNode(fmt.Sprintf("node-%d", i)); err != nil {
				t.Errorf("AddNode returned error: %v", err)
			}
		}(i)
	}

	select {
	case <-g.arrived:
		// All `concurrency` goroutines are concurrently inside the lock-free
		// GetNode call: the critical section is no longer serializing them.
	case <-time.After(5 * time.Second):
		close(g.release) // unblock so the goroutines can finish and not leak
		wg.Wait()
		t.Fatalf("AddNode did not run %d calls concurrently; the lock-free path appears serialized", concurrency)
	}

	close(g.release)
	wg.Wait()

	assert.Equal(t, concurrency, g.maxInFlight, "all goroutines should be inside the lock-free section simultaneously")
	assert.Len(t, m.dataStore, concurrency)
}
