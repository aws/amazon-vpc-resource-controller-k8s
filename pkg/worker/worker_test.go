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
	"context"
	"github.com/stretchr/testify/assert"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"testing"
	"time"
)

var (
	resourceName = "vpc.amazonaws.com/pod-eni"
	bufferSize = 2
	workerCount = 1
	workerMockProcessTime = time.Millisecond * 100
)

func GetMockWorkerPool(ctx context.Context) *Worker {
	log := zap.New(zap.UseDevMode(true)).WithValues("worker resource Id", resourceName)
	return NewWorkerPool(bufferSize, resourceName, workerCount, MockWorkerFunc, log, ctx)
}

func MockWorkerFunc(job interface{}) {
	v := job.(*int)
	*v++
	time.Sleep(workerMockProcessTime)
}

func TestNewWorkerPool(t *testing.T) {
	ctx, _ := context.WithCancel(context.Background())
	w := GetMockWorkerPool(ctx)
	assert.NotNil(t, w)
}

// TestWorker_SubmitJob verifies that jobs are accepted and completed if the buffer is not full.
func TestWorker_SubmitJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := GetMockWorkerPool(ctx)
	w.StartWorkers()

	// Count to verify
	var	jobCompletedCounter = 0

	// Submit two jobs
	var jobCount = 2
	for i:=0 ; i < jobCount; i++ {
		err := w.SubmitJob(&jobCompletedCounter)
		assert.NoError(t, err)
	}

	// Wait till the job complete. If the test is flaky, increase the buffer sleep time.
	time.Sleep(workerMockProcessTime * time.Duration(jobCount + 1))

	// Verify job completed.
	assert.Equal(t, jobCount, jobCompletedCounter)
}

// TestWorker_SubmitJob_BufferOverflow verifies that if the buffer is full the new job is rejected with an error.
func TestWorker_SubmitJob_BufferOverflow(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := GetMockWorkerPool(ctx)
	w.StartWorkers()

	// Count to verify
	var	jobCompletedCounter = 0

	// Submit 2 jobs
	var jobCount = 2
	for i:=0 ; i < jobCount; i++ {
		err := w.SubmitJob(&jobCompletedCounter)
		assert.NoError(t, err)
	}

	// Submit one more job and verify buffer overflows
	err := w.SubmitJob(&jobCompletedCounter)
	assert.Error(t, err)

	// Wait till the job complete. If the test is flaky, increase the buffer sleep time.
	time.Sleep(workerMockProcessTime * time.Duration(jobCount + 1))

	// Verify job completed.
	assert.Equal(t, jobCount, jobCompletedCounter)
}
