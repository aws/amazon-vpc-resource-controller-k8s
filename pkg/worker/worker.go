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

package worker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// Prometheus metrics
var (
	prometheusRegistered = false

	// summaryObjectives mirrors the objectives map used by branch_provider_operation_latency
	// (pkg/provider/branch/provider.go) so all controller latency summaries share the same quantiles.
	summaryObjectives = map[float64]float64{0: 0, 0.5: 0.05, 0.9: 0.01, 0.99: 0.001, 1: 0}

	workerQueueDepth = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "worker_queue_depth",
			Help: "The current number of jobs in the worker queue",
		},
		[]string{"resource_name"},
	)

	workerQueueWaitLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "worker_queue_wait_latency",
			Help:       "Time in seconds a job waits in the worker queue from enqueue to worker pickup",
			Objectives: summaryObjectives,
		},
		[]string{"resource_name"},
	)

	workerJobProcessLatency = prometheus.NewSummaryVec(
		prometheus.SummaryOpts{
			Name:       "worker_job_process_latency",
			Help:       "Worker-side job processing time in seconds",
			Objectives: summaryObjectives,
		},
		[]string{"resource_name"},
	)

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

	jobsNotFoundCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "jobs_resource_not_found_count",
			Help: "The number of jobs whose resources were not found",
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
	SubmitJobAfter(job interface{}, submitAfter time.Duration)
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
	// enqueueTimes records, per queued job, the time it was submitted so the queue-wait latency can be
	// observed at pickup. It is kept as a sidecar map rather than wrapping the queued item so that queue
	// item identity (and therefore dedup/rate-limiter keying) is unchanged. Guarded by enqueueTimesLock.
	enqueueTimes     map[interface{}]time.Time
	enqueueTimesLock sync.Mutex
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
		enqueueTimes:    make(map[interface{}]time.Time),
	}
}

// prometheusRegister registers the metrics.
func prometheusRegister() {
	if !prometheusRegistered {
		metrics.Registry.MustRegister(
			jobsSubmittedCount,
			jobsCompletedCount,
			jobsFailedCount,
			jobsNotFoundCount,
			workerQueueDepth,
			workerQueueWaitLatency,
			workerJobProcessLatency)

		prometheusRegistered = true
	}
}

func (w *worker) SetWorkerFunc(workerFunc func(interface{}) (ctrl.Result, error)) {
	w.workerFunc = workerFunc
}

// SubmitJob adds the job to the rate limited queue
func (w *worker) SubmitJob(job interface{}) {
	// in theory, only health check endpoint should send a nil job to test periodically
	if job == nil {
		queueLen := w.queue.Len()
		w.Log.V(1).Info("For informational / health check purpose only to check worker queue availability", "WorkerQueueLen", queueLen)
		return
	}
	w.recordEnqueueTime(job)
	w.queue.Add(job)
	jobsSubmittedCount.WithLabelValues(w.resourceName).Inc()
	workerQueueDepth.WithLabelValues(w.resourceName).Set(float64(w.queue.Len()))
}

// SubmitJobAfter submits the job to the work queue after the given time period
func (w *worker) SubmitJobAfter(job interface{}, submitAfter time.Duration) {
	w.recordEnqueueTime(job)
	w.queue.AddAfter(job, submitAfter)
	jobsSubmittedCount.WithLabelValues(w.resourceName).Inc()
	workerQueueDepth.WithLabelValues(w.resourceName).Set(float64(w.queue.Len()))
}

// recordEnqueueTime stamps the current time against a job so the queue-wait latency can be measured when
// the job is later picked up. Metrics-only: it does not affect queue item identity or ordering.
func (w *worker) recordEnqueueTime(job interface{}) {
	w.enqueueTimesLock.Lock()
	defer w.enqueueTimesLock.Unlock()
	w.enqueueTimes[job] = time.Now()
}

// popEnqueueTime returns and clears the recorded enqueue time for a job, if one was recorded.
func (w *worker) popEnqueueTime(job interface{}) (time.Time, bool) {
	w.enqueueTimesLock.Lock()
	defer w.enqueueTimesLock.Unlock()
	t, ok := w.enqueueTimes[job]
	if ok {
		delete(w.enqueueTimes, job)
	}
	return t, ok
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
	// A job has been dequeued, reflect the reduced depth for this pool.
	workerQueueDepth.WithLabelValues(w.resourceName).Set(float64(w.queue.Len()))
	// Observe how long the job waited between enqueue and pickup, if the enqueue time was recorded.
	if enqueuedAt, ok := w.popEnqueueTime(job); ok {
		workerQueueWaitLatency.WithLabelValues(w.resourceName).Observe(time.Since(enqueuedAt).Seconds())
	}
	log := w.Log.WithValues("job", job)

	cont = true

	processStart := time.Now()
	result, err := w.workerFunc(job)
	workerJobProcessLatency.WithLabelValues(w.resourceName).Observe(time.Since(processStart).Seconds())

	if err != nil {
		if w.queue.NumRequeues(job) >= w.maxRetriesOnErr {
			log.Error(err, "exceeded maximum retries", "max retries", w.maxRetriesOnErr)
			w.queue.Forget(job)
			jobsFailedCount.WithLabelValues(w.resourceName).Inc()
			return
		} else if apierrors.IsNotFound(err) {
			//similar to upstream https://github.com/kubernetes-sigs/controller-runtime/issues/377#issue-426207628
			log.Error(err, "won't requeue a not found errored job", "job", job)
			w.queue.Forget(job)
			jobsNotFoundCount.WithLabelValues(w.resourceName).Inc()
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
