/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package worker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var (
	podName      = "pod-name"
	podNamespace = "pod-namespace"
	podUid       = "pod-uid"
	reqCount     = 2
	nodeName     = "node-name"
)

// TestNewOnDemandCreateJob tests the fields of Create Job
func TestNewOnDemandCreateJob(t *testing.T) {
	onDemandJob := NewOnDemandCreateJob(podUid, podNamespace, podName, reqCount)

	assert.Equal(t, OperationCreate, onDemandJob.Operation)
	assert.Equal(t, podUid, string(onDemandJob.UID))
	assert.Equal(t, podName, onDemandJob.PodName)
	assert.Equal(t, podNamespace, onDemandJob.PodNamespace)
	assert.Equal(t, reqCount, onDemandJob.RequestCount)
}

// TestNewOnDemandDeleteJob tests the fields of Deleted Job
func TestNewOnDemandDeletedJob(t *testing.T) {
	onDemandJob := NewOnDemandDeletedJob(podNamespace, podName)

	assert.Equal(t, OperationDeleted, onDemandJob.Operation)
	assert.Equal(t, podName, onDemandJob.PodName)
	assert.Equal(t, podNamespace, onDemandJob.PodNamespace)
}

func TestNewOnDemandDeletingJob(t *testing.T) {
	onDemandJob := NewOnDemandDeletingJob(podUid, podNamespace, podName, nodeName)

	assert.Equal(t, OperationDeleting, onDemandJob.Operation)
	assert.Equal(t, podUid, string(onDemandJob.UID))
	assert.Equal(t, podNamespace, onDemandJob.PodNamespace)
	assert.Equal(t, podName, onDemandJob.PodName)
	assert.Equal(t, nodeName, onDemandJob.NodeName)
}

func TestNewOnDemandReconcileJob(t *testing.T) {
	onDemandJob := NewOnDemandReconcileJob(nodeName)

	assert.Equal(t, OperationReconcile, onDemandJob.Operation)
	assert.Equal(t, nodeName, onDemandJob.NodeName)
}

func TestNewOnDemandProcessDeleteQueueJob(t *testing.T) {
	onDemandJob := NewOnDemandProcessDeleteQueueJob(nodeName)

	assert.Equal(t, OperationProcessDeleteQueue, onDemandJob.Operation)
	assert.Equal(t, nodeName, onDemandJob.NodeName)
}
