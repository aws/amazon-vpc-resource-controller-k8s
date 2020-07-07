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
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
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

	jobsFailedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jobs_failed_count",
			Help: "The number of jobs that failed to complete after retries",
		}, []string{"resource"},
	)
)

// Errors
var (
	WorkersAlreadyStartedError = errors.New("failed to start the workers as they are already running")
)

type Worker interface {
	StartWorkerPool(func(interface{}) (ctrl.Result, error)) error
	SubmitJob(job interface{})
}

type worker struct {
	// resourceName that the worker belongs to
	resourceName string
	// workersStarted is the flag to prevent starting duplicate set of workers
	workersStarted bool
	// workerFunc is the function that will be invoked with the job by the worker routine
	workerFunc func(interface{}) (ctrl.Result, error)
	// maxRetries is the number of times to retry item in case of failure
	maxRetriesOnErr int
	// maxWorkerCount represents the maximum number of workers that will be started
	maxWorkerCount int
	// ctx is the background context to close the chanel on termination signal
	ctx context.Context
	// Log is the structured logger set to log with resource name
	Log logr.Logger
	// queue is the k8s rate limiting queue to store the submitted jobs
	queue workqueue.RateLimitingInterface
}

// NewDefaultWorkerPool returns a new worker pool for a give resource type with the given configuration
func NewDefaultWorkerPool(resourceName string, workerCount int, maxRequeue int,
	logger logr.Logger, ctx context.Context) Worker {

	prometheusRegister()

	return &worker{
		resourceName:    resourceName,
		maxRetriesOnErr: maxRequeue,
		maxWorkerCount:  workerCount,
		Log:             logger,
		queue:           workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		ctx:             ctx,
	}
}

// prometheusRegister registers the metrics.
func prometheusRegister() {
	if !prometheusRegistered {
		metrics.Registry.MustRegister(
			jobsSubmittedCount,
			jobsCompletedCount,
			jobsFailedCount)

		prometheusRegistered = true
	}
}

func (w *worker) SetWorkerFunc(workerFunc func(interface{}) (ctrl.Result, error)) {
	w.workerFunc = workerFunc
}

// SubmitJob adds the job to the rate limited queue
func (w *worker) SubmitJob(job interface{}) {
	w.queue.Add(job)
	jobsSubmittedCount.WithLabelValues(w.resourceName).Inc()
}

// runWorker runs a worker that listens on new item on the worker queue
func (w *worker) runWorker() {
	for w.processNextItem() {
	}
}

// processNextItem returns false if the queue is shut down, otherwise processes the job and returns true
func (w *worker) processNextItem() (cont bool) {
	job, quit := w.queue.Get()
	if quit {
		return
	}
	defer w.queue.Done(job)
	log := w.Log.WithValues("job", job)

	cont = true

	if result, err := w.workerFunc(job); err != nil {
		if w.queue.NumRequeues(job) >= w.maxRetriesOnErr {
			log.Error(err, "exceeded maximum retries", "max retries", w.maxRetriesOnErr)
			w.queue.Forget(job)
			jobsFailedCount.WithLabelValues(w.resourceName).Inc()
			return
		}
		log.Error(err, "re-queuing job", "retry count", w.queue.NumRequeues(job))
		w.queue.AddRateLimited(job)
		return
	} else if result.Requeue {
		log.V(1).Info("timed retry", "retry after", result.RequeueAfter)
		w.queue.AddAfter(job, result.RequeueAfter)
		return
	}

	log.V(1).Info("completed job successfully")

	w.queue.Forget(job)
	jobsCompletedCount.WithLabelValues(w.resourceName).Inc()

	return
}

// StartWorkerPool starts the worker pool that starts the worker routines that concurrently listen on the channel
func (w *worker) StartWorkerPool(workerFunc func(interface{}) (ctrl.Result, error)) error {
	if w.workersStarted {
		return WorkersAlreadyStartedError
	}
	w.workerFunc = workerFunc
	w.workersStarted = true

	go func() {
		w.Log.Info("starting routine to listen on chanel for termination signal")
		<-w.ctx.Done()
		w.queue.ShutDown()
		w.Log.Info("shut down the queue after receiving termination signal")
	}()

	w.Log.Info("starting worker routines", "worker count", w.maxWorkerCount)

	// Start a new go routine to listen on the chanel and allocate jobs to go routines
	for workerCount := 1; workerCount <= w.maxWorkerCount; workerCount++ {
		go w.runWorker()
	}

	return nil
}
