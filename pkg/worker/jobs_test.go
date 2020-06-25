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
	podName = "pod-name"
	podNamespace = "pod-namespace"
	reqCount = int64(2)
)

// TestNewOnDemandCreateJob tests the fields of Create Job
func TestNewOnDemandCreateJob(t *testing.T) {
	onDemandJob := NewOnDemandCreateJob(podNamespace, podName, reqCount)

	assert.Equal(t, OperationCreate, onDemandJob.Operation)
	assert.Equal(t, podName, onDemandJob.PodName)
	assert.Equal(t, podNamespace, onDemandJob.PodNamespace)
	assert.Equal(t, reqCount, onDemandJob.RequestCount)
}

// TestNewOnDemandDeleteJob tests the fields of Delete Job
func TestNewOnDemandDeleteJob(t *testing.T) {
	onDemandJob := NewOnDemandDeleteJob(podNamespace, podName)

	assert.Equal(t, OperationDelete, onDemandJob.Operation)
	assert.Equal(t, podName, onDemandJob.PodName)
	assert.Equal(t, podNamespace, onDemandJob.PodNamespace)
}
