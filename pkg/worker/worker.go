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
	"errors"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
)

// Prometheus metrics
var (
	prometheusRegistered = false

	jobsSubmittedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jobs_submitted_count",
			Help: "The number of jobs submitted to the buffer",
		},
		[]string{"resource"},
	)

	jobsCompletedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jobs_completed_count",
			Help: "The number of jobs completed by worker routines",
		},
		[]string{"resource"},
	)

	jobsRejectedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jobs_rejected_count",
			Help: "The number of jobs rejected due to buffer overflow",
		},
		[]string{"resource"},
	)
)

// Errors
var (
	BufferOverflowError        = errors.New("failed to accept new jobs to the buffer, buffer is at full capacity")
	WorkersAlreadyStartedError = errors.New("failed to start the workers as they are already running")
)

type Worker struct {
	// resourceName that the worker belongs to
	resourceName string
	// workersStarted is the flag to prevent starting duplicate set of workers
	workersStarted bool
	// workerFunc is the function that will be invoked with the job by the worker routine
	workerFunc func(interface{})
	// buffer is the channel holding the jobs that will be processed by workers
	buffer chan interface{}
	// maxWorkerCount represents the maximum number of workers that will be started
	maxWorkerCount int
	// ctx is the background context to close the chanel on termination signal
	ctx context.Context
	// Log is the structured logger set to log with resource name
	Log logr.Logger
}

// NewWorkerPool returns a new worker pool for a give resource type with the given configuration
func NewWorkerPool(bufferSize int, resourceName string,
	workerCount int, workerFunc func(interface{}),
	logger logr.Logger, ctx context.Context) *Worker {

	prometheusRegister()

	return &Worker{
		resourceName:   resourceName,
		Log:            logger,
		workerFunc:     workerFunc,
		maxWorkerCount: workerCount,
		ctx:            ctx,
		buffer:         make(chan interface{}, bufferSize),
	}
}

// prometheusRegister registers the metrics.
func prometheusRegister() {
	if !prometheusRegistered {
		prometheus.MustRegister(jobsSubmittedCount)
		prometheus.MustRegister(jobsCompletedCount)
		prometheus.MustRegister(jobsRejectedCount)

		prometheusRegistered = true
	}
}

// SubmitJob takes a job to be added to the buffer, if the buffer is already full it will return
// error and it's upto the caller to handle the overflow item
func (w *Worker) SubmitJob(job interface{}) error {
	jobsSubmittedCount.WithLabelValues(w.resourceName).Inc()
	if len(w.buffer) == cap(w.buffer) {
		jobsRejectedCount.WithLabelValues(w.resourceName).Inc()
		w.Log.Error(BufferOverflowError, "cannot accept any more jobs",
			"buffer size", len(w.buffer))
		return BufferOverflowError
	}

	w.buffer <- job
	return nil
}

// startWorker starts a worker routine that listens on the buffer to process new jobs
func (w *Worker) startWorker(id int, jobs <-chan interface{}) {
	for job := range jobs {
		w.Log.Info("starting job", "worker id", id, "job", job)
		w.workerFunc(job)
		jobsCompletedCount.WithLabelValues(w.resourceName).Inc()
	}
}

// StartWorkerPool starts the worker pool that starts the worker routines that concurrently listen on the channel
func (w *Worker) StartWorkerPool() error {
	if w.workersStarted {
		return WorkersAlreadyStartedError
	}

	w.workersStarted = true

	go func() {
		w.Log.Info("starting routine to listen on chanel for termination signal")
		<-w.ctx.Done()
		close(w.buffer)
		w.Log.Info("closed the buffer after receiving termination signal")
	}()

	w.Log.Info("starting worker routines", "buffer size", cap(w.buffer),
		"worker count", w.maxWorkerCount)

	// Start a new go routine to listen on the chanel and allocate jobs to go routines
	for workerCount := 1; workerCount <= w.maxWorkerCount; workerCount++ {
		go w.startWorker(workerCount, w.buffer)
	}

	return nil
}
