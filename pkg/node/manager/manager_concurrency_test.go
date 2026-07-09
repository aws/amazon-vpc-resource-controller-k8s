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

package manager

import (
	"sync"
	"testing"

	rcV1alpha1 "github.com/aws/amazon-vpc-resource-controller-k8s/apis/vpcresources/v1alpha1"
	"github.com/aws/amazon-vpc-resource-controller-k8s/pkg/node"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// Test_ConcurrentNodeOps hammers AddNode/UpdateNode/DeleteNode/GetNode for the same
// node name from many goroutines at once. Its purpose is to exercise the narrowed lock
// scope for data races (run with -race) and lock-order inversions (run with -tags deadlock):
//
//	go test -race ./pkg/node/manager/...
//	go test -tags deadlock -race ./pkg/node/manager/...
//
// The narrowed AddNode/UpdateNode do k8s reads outside the manager lock and take the lock
// only for the dataStore map mutation, using double-checked / re-checked membership. If any
// path mutates the map without the lock, -race flags it; if the manager lock is ever held
// while a node's own lock is taken in a conflicting order, -tags deadlock flags it. The test
// passing under both is the safety evidence for the lock-narrowing change.
func Test_ConcurrentNodeOps(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	// All collaborators accept calls any number of times, in any order, from any goroutine.
	// gomock dispatch of already-registered expectations is safe under concurrent calls.
	mock.MockK8sAPI.EXPECT().GetNode(gomock.Any()).Return(v1Node, nil).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(&rcV1alpha1.CNINode{}, nil).AnyTimes()
	mock.MockK8sAPI.EXPECT().CreateCNINode(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mock.MockWorker.EXPECT().SubmitJob(gomock.Any()).AnyTimes()

	const goroutines = 32
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				switch (g + i) % 4 {
				case 0:
					// AddNode may error if the node was concurrently removed from the
					// k8s cache mock (it won't here) - errors are fine, races are not.
					_ = mock.Manager.AddNode(nodeName)
				case 1:
					_ = mock.Manager.UpdateNode(nodeName)
				case 2:
					_ = mock.Manager.DeleteNode(nodeName)
				case 3:
					_, _ = mock.Manager.GetNode(nodeName)
				}
			}
		}(g)
	}

	wg.Wait()

	// The datastore must be internally consistent afterward: whatever GetNode returns
	// must be reachable without panic, and a final read must not race a concurrent op
	// (the WaitGroup already joined all workers, so this is a quiescent read).
	if n, found := mock.Manager.GetNode(nodeName); found {
		assert.NotNil(t, n)
	}
}

// Test_ConcurrentAddSameNode_SingleSubmit asserts the double-checked locking in AddNode:
// when many goroutines race to add the SAME new node, the async Init job is submitted
// exactly once (the map check-then-write is atomic under the write lock), not once per
// goroutine. This is the specific correctness property the lock protects after narrowing.
func Test_ConcurrentAddSameNode_SingleSubmit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mock := NewMock(ctrl, map[string]node.Node{})

	mock.MockK8sAPI.EXPECT().GetNode(gomock.Any()).Return(v1Node, nil).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(&rcV1alpha1.CNINode{}, nil).AnyTimes()
	mock.MockK8sAPI.EXPECT().CreateCNINode(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	// SubmitJob must be called exactly once across all racing adds of the same node.
	var submitMu sync.Mutex
	submitCount := 0
	mock.MockWorker.EXPECT().SubmitJob(gomock.Any()).DoAndReturn(func(_ interface{}) {
		submitMu.Lock()
		submitCount++
		submitMu.Unlock()
	}).AnyTimes()

	const goroutines = 24
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			_ = mock.Manager.AddNode(nodeName)
		}()
	}
	wg.Wait()

	assert.Equal(t, 1, submitCount, "racing AddNode of the same new node must submit the Init job exactly once")
	_, found := mock.Manager.GetNode(nodeName)
	assert.True(t, found, "node must be present in the datastore after concurrent adds")
}

// Test_ConcurrentGetNode_NoRace is a lightweight reader-side race check: many concurrent
// GetNode reads against a pre-populated datastore while writers add/delete. Ensures the
// RLock-only read path in the narrowed lock is race-free.
func Test_ConcurrentGetNode_NoRace(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	managedNode := node.NewManagedNode(zap.New(), nodeName, instanceID, "linux", nil, nil)
	mock := NewMock(ctrl, map[string]node.Node{nodeName: managedNode})

	mock.MockK8sAPI.EXPECT().GetNode(gomock.Any()).Return(v1Node, nil).AnyTimes()
	mock.MockK8sAPI.EXPECT().GetCNINode(gomock.Any()).Return(&rcV1alpha1.CNINode{}, nil).AnyTimes()
	mock.MockK8sAPI.EXPECT().CreateCNINode(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	mock.MockWorker.EXPECT().SubmitJob(gomock.Any()).AnyTimes()

	// Silence unused import in case the ctrl types shift; types is used by other files.
	_ = types.NamespacedName{}

	const readers = 24
	const writers = 4
	var wg sync.WaitGroup
	wg.Add(readers + writers)

	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				_, _ = mock.Manager.GetNode(nodeName)
			}
		}()
	}
	for w := 0; w < writers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				if i%2 == 0 {
					_ = mock.Manager.AddNode(nodeName)
				} else {
					_ = mock.Manager.DeleteNode(nodeName)
				}
			}
		}()
	}
	wg.Wait()
}
