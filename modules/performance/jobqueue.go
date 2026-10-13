package performance

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
)

// ErrJobExists is returned by PushWithOptions when a job is already stored
// under the ID it was given.
var ErrJobExists = errors.New("job ID already exists")

// JobQueue runs jobs stored in a KV store, with scheduled starts and retries.
// Built-in stores claim jobs atomically across queue instances sharing a
// backend. A custom store needs hyperchi.AtomicTakeStore for that; otherwise
// claims are serialized only among this JobQueue's workers. A claim deletes
// the pending entry, so a job claimed by a process that then crashes is lost:
// there is no recovery and no exactly-once guarantee.
type JobQueue struct {
	store         hyperchi.KVStore
	keyPrefix     string
	workers       int
	retryLimit    int
	retryDelay    time.Duration
	maxRetryDelay time.Duration
	processingTTL time.Duration
	handlers      map[string]JobHandler
	now           func() time.Time // time.Now; tests replace it
	claimMu       sync.Mutex       // serializes claims on stores without AtomicTakeStore
	pushMu        sync.Mutex       // serializes the duplicate ID check with the write that follows it
	mu            sync.RWMutex

	lifecycle sync.Mutex // guards started, stopped, and the WaitGroup's Add
	started   bool
	stopped   bool
	stopChan  chan struct{}
	wg        sync.WaitGroup
}

// JobQueueConfig configures a JobQueue. Zero values take the defaults shown.
type JobQueueConfig struct {
	Backend       string        `json:"backend"`         // name of a store in the KV registry
	KeyPrefix     string        `json:"key_prefix"`      // default "jobs:"
	Workers       int           `json:"workers"`         // worker goroutines, default 4
	RetryLimit    int           `json:"retry_limit"`     // default MaxAttempts per job, default 3
	RetryDelay    time.Duration `json:"retry_delay"`     // wait before the first retry, doubled for each later one, default 1 minute
	MaxRetryDelay time.Duration `json:"max_retry_delay"` // cap on the doubled wait, default 1 hour
	ProcessingTTL time.Duration `json:"processing_ttl"`  // lifetime of the in-progress record, default 5 minutes
}

// Job is one unit of work and its state, stored as JSON.
type Job struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Payload     map[string]any `json:"payload"`
	Priority    int            `json:"priority"` // higher runs first among jobs that are due
	Attempts    int            `json:"attempts"`
	MaxAttempts int            `json:"max_attempts"`
	CreatedAt   time.Time      `json:"created_at"`
	ScheduledAt time.Time      `json:"scheduled_at"` // earliest start
	StartedAt   *time.Time     `json:"started_at,omitempty"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
	FailedAt    *time.Time     `json:"failed_at,omitempty"`
	Error       string         `json:"error,omitempty"`
	Status      JobStatus      `json:"status"`
}

// JobStatus is the state of a Job.
type JobStatus string

// Job states. A job in JobStatusRetrying waits in the pending queue until its
// ScheduledAt.
const (
	JobStatusPending    JobStatus = "pending"
	JobStatusProcessing JobStatus = "processing"
	JobStatusCompleted  JobStatus = "completed"
	JobStatusFailed     JobStatus = "failed"
	JobStatusRetrying   JobStatus = "retrying"
)

// JobHandler runs one job. A returned error counts as a failed attempt.
type JobHandler func(job *Job) error

// JobResult records how a successful job ran. Stores with TTLs keep it for
// 24 hours.
type JobResult struct {
	JobID     string        `json:"job_id"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
	Duration  time.Duration `json:"duration"`
	Timestamp time.Time     `json:"timestamp"`
}

// NewJobQueue returns a stopped queue over the store named config.Backend.
// Register handlers, then call Start.
func NewJobQueue(registry *hyperchi.KVRegistry, config JobQueueConfig) (*JobQueue, error) {
	store, err := registry.Get(config.Backend)
	if err != nil {
		return nil, fmt.Errorf("failed to get KV store '%s': %w", config.Backend, err)
	}

	if config.KeyPrefix == "" {
		config.KeyPrefix = "jobs:"
	}
	if config.Workers == 0 {
		config.Workers = 4
	}
	if config.RetryLimit == 0 {
		config.RetryLimit = 3
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = time.Minute
	}
	if config.MaxRetryDelay == 0 {
		config.MaxRetryDelay = time.Hour
	}
	if config.ProcessingTTL == 0 {
		config.ProcessingTTL = 5 * time.Minute
	}

	return &JobQueue{
		store:         store,
		keyPrefix:     config.KeyPrefix,
		workers:       config.Workers,
		retryLimit:    config.RetryLimit,
		retryDelay:    config.RetryDelay,
		maxRetryDelay: config.MaxRetryDelay,
		processingTTL: config.ProcessingTTL,
		handlers:      make(map[string]JobHandler),
		now:           time.Now,
		stopChan:      make(chan struct{}),
	}, nil
}

// RegisterHandler sets the handler for jobType. It is safe to call while the
// queue runs; a job whose type has no handler fails without retries.
func (jq *JobQueue) RegisterHandler(jobType string, handler JobHandler) {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	jq.handlers[jobType] = handler
}

// Push queues a job of jobType to run as soon as a worker is free.
func (jq *JobQueue) Push(jobType string, payload map[string]any) (*Job, error) {
	return jq.PushWithOptions(jobType, payload, JobOptions{})
}

// JobOptions adjusts a job created by PushWithOptions.
type JobOptions struct {
	Priority    int        `json:"priority"`     // higher runs first among due jobs; ties go to the earliest ScheduleAt
	MaxAttempts int        `json:"max_attempts"` // 0 uses the queue's RetryLimit
	ScheduleAt  *time.Time `json:"schedule_at"`  // nil runs it now
	ID          string     `json:"id"`           // empty generates the type, "-", and 26 random base32 characters; an ID already stored is ErrJobExists
}

// PushWithOptions stores a new job and adds it to the pending queue. It
// returns ErrJobExists when a job, in any status, is already stored under the
// ID. The check is atomic among pushes through this JobQueue only: two
// queues sharing a backend can still both store the same ID.
func (jq *JobQueue) PushWithOptions(jobType string, payload map[string]any, options JobOptions) (*Job, error) {
	now := jq.now()

	jobID := options.ID
	if jobID == "" {
		// Random, not time-based: pushes in the same clock tick must not
		// share an ID, or the later one overwrites the earlier job.
		jobID = jobType + "-" + rand.Text()
	}

	maxAttempts := options.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = jq.retryLimit
	}

	scheduledAt := now
	if options.ScheduleAt != nil {
		scheduledAt = *options.ScheduleAt
	}

	job := &Job{
		ID:          jobID,
		Type:        jobType,
		Payload:     payload,
		Priority:    options.Priority,
		Attempts:    0,
		MaxAttempts: maxAttempts,
		CreatedAt:   now,
		ScheduledAt: scheduledAt,
		Status:      JobStatusPending,
	}

	jq.pushMu.Lock()
	defer jq.pushMu.Unlock()
	switch _, err := jq.store.Get(jq.getJobKey(jobID)); {
	case err == nil:
		return nil, fmt.Errorf("push job %q: %w", jobID, ErrJobExists)
	case !errors.Is(err, hyperchi.ErrKeyNotFound):
		return nil, fmt.Errorf("check job ID %q: %w", jobID, err)
	}

	if err := jq.storeJob(job); err != nil {
		return nil, fmt.Errorf("failed to store job: %w", err)
	}

	queueKey := jq.getQueueKey(JobStatusPending)
	jobRef := JobReference{
		ID:          job.ID,
		Priority:    job.Priority,
		ScheduledAt: job.ScheduledAt,
	}

	if err := jq.addToQueue(queueKey, jobRef); err != nil {
		return nil, fmt.Errorf("failed to add job to queue: %w", err)
	}

	return job, nil
}

// JobReference is the pending-queue entry that points at a stored Job.
type JobReference struct {
	ID          string    `json:"id"`
	Priority    int       `json:"priority"`
	ScheduledAt time.Time `json:"scheduled_at"`
}

// Start launches the workers. Each polls for a job once a second. Later
// calls do nothing, and a stopped queue does not restart.
func (jq *JobQueue) Start() {
	jq.lifecycle.Lock()
	defer jq.lifecycle.Unlock()
	if jq.started || jq.stopped {
		return
	}
	jq.started = true
	log.Printf("Starting job queue with %d workers", jq.workers)

	for i := 0; i < jq.workers; i++ {
		jq.wg.Add(1)
		go jq.worker(i)
	}
}

// Stop signals the workers and waits for them to finish their current job.
// It is safe to call more than once, and before Start.
func (jq *JobQueue) Stop() {
	jq.lifecycle.Lock()
	if !jq.stopped {
		jq.stopped = true
		close(jq.stopChan)
	}
	jq.lifecycle.Unlock()
	jq.wg.Wait()
}

// worker polls for jobs until Stop.
func (jq *JobQueue) worker(workerID int) {
	defer jq.wg.Done()

	log.Printf("Worker %d started", workerID)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-jq.stopChan:
			log.Printf("Worker %d stopping", workerID)
			return
		case <-ticker.C:
			if err := jq.processNextJob(workerID); err != nil {
				if err != hyperchi.ErrKeyNotFound {
					log.Printf("Worker %d error: %v", workerID, err)
				}
			}
		}
	}
}

// processNextJob claims one pending job and runs it. It returns
// hyperchi.ErrKeyNotFound when nothing is pending.
func (jq *JobQueue) processNextJob(workerID int) error {
	job, err := jq.getNextJob()
	if err != nil {
		return err
	}

	jq.mu.RLock()
	handler, exists := jq.handlers[job.Type]
	jq.mu.RUnlock()

	if !exists {
		return jq.failJob(job, fmt.Errorf("no handler registered for job type '%s'", job.Type))
	}

	job.Status = JobStatusProcessing
	job.StartedAt = &[]time.Time{time.Now()}[0]
	job.Attempts++

	if err := jq.storeJob(job); err != nil {
		// The claim removed the pending entry. Put it back so the job is not lost.
		return errors.Join(fmt.Errorf("failed to update job status: %w", err), jq.requeueJob(job))
	}

	// The in-progress record is informational, so a failure to write it does
	// not stop the job.
	if err := jq.addToProcessingQueue(job); err != nil {
		log.Printf("performance: job %s: writing the in-progress record: %v", job.ID, err)
	}

	log.Printf("Worker %d processing job %s (type: %s, attempt: %d)",
		workerID, job.ID, job.Type, job.Attempts)

	start := time.Now()
	err = handler(job)
	duration := time.Since(start)

	result := &JobResult{
		JobID:     job.ID,
		Success:   err == nil,
		Duration:  duration,
		Timestamp: time.Now(),
	}

	if err != nil {
		result.Error = err.Error()
		log.Printf("Worker %d job %s failed: %v", workerID, job.ID, err)
		return jq.handleJobFailure(job, err)
	}

	log.Printf("Worker %d job %s completed in %v", workerID, job.ID, duration)
	return jq.completeJob(job, result)
}

// getNextJob claims the pending job that is due (ScheduledAt not in the
// future) with the highest Priority, breaking ties by the earliest
// ScheduledAt and then by key, and loads it. It returns
// hyperchi.ErrKeyNotFound when no job is due.
func (jq *JobQueue) getNextJob() (*Job, error) {
	jq.claimMu.Lock()
	defer jq.claimMu.Unlock()

	for {
		refKey, err := jq.pickDueJob()
		if err != nil {
			return nil, err
		}

		refData, err := jq.take(refKey)
		if errors.Is(err, hyperchi.ErrKeyNotFound) {
			continue // another queue claimed it first
		}
		if err != nil {
			return nil, err
		}

		var jobRef JobReference
		if err := json.Unmarshal(refData, &jobRef); err != nil {
			return nil, fmt.Errorf("queue entry %q: %w", refKey, err)
		}
		return jq.getJob(jobRef.ID)
	}
}

// pickDueJob returns the key of the pending entry getNextJob should claim.
// An entry that cannot be decoded is logged and skipped, so one bad record
// does not stop the queue.
func (jq *JobQueue) pickDueJob() (string, error) {
	prefix := jq.getQueueKey(JobStatusPending) + ":"
	keys, err := jq.store.Keys(prefix)
	if err != nil {
		return "", err
	}

	now := time.Now()
	var bestKey string
	var best JobReference
	for _, key := range keys {
		data, err := jq.store.Get(key)
		if errors.Is(err, hyperchi.ErrKeyNotFound) {
			continue // claimed since Keys listed it
		}
		if err != nil {
			return "", err
		}
		var ref JobReference
		if err := json.Unmarshal(data, &ref); err != nil {
			log.Printf("performance: skipping unreadable queue entry %q: %v", key, err)
			continue
		}
		if ref.ScheduledAt.After(now) {
			continue
		}
		if bestKey == "" || runsBefore(ref, key, best, bestKey) {
			bestKey, best = key, ref
		}
	}

	if bestKey == "" {
		return "", hyperchi.ErrKeyNotFound
	}
	return bestKey, nil
}

// runsBefore orders due jobs: higher Priority, then earlier ScheduledAt, then key.
func runsBefore(a JobReference, aKey string, b JobReference, bKey string) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	if !a.ScheduledAt.Equal(b.ScheduledAt) {
		return a.ScheduledAt.Before(b.ScheduledAt)
	}
	return aKey < bKey
}

// take removes key and returns its value: atomically on an
// hyperchi.AtomicTakeStore, and under claimMu otherwise.
func (jq *JobQueue) take(key string) ([]byte, error) {
	if store, ok := jq.store.(hyperchi.AtomicTakeStore); ok {
		return store.GetAndDelete(key)
	}
	data, err := jq.store.Get(key)
	if err != nil {
		return nil, err
	}
	if err := jq.store.Delete(key); err != nil {
		return nil, err
	}
	return data, nil
}

// getJob loads the job stored under jobID.
func (jq *JobQueue) getJob(jobID string) (*Job, error) {
	jobKey := jq.getJobKey(jobID)
	data, err := jq.store.Get(jobKey)
	if err != nil {
		return nil, err
	}

	var job Job
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, err
	}

	return &job, nil
}

// storeJob saves job as JSON under its ID.
func (jq *JobQueue) storeJob(job *Job) error {
	jobKey := jq.getJobKey(job.ID)
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}

	return jq.store.Set(jobKey, data)
}

// completeJob marks job completed and stores its result.
func (jq *JobQueue) completeJob(job *Job, result *JobResult) error {
	now := time.Now()
	job.Status = JobStatusCompleted
	job.CompletedAt = &now

	if err := jq.storeJob(job); err != nil {
		return err
	}

	return errors.Join(jq.removeFromProcessingQueue(job), jq.storeJobResult(result))
}

// handleJobFailure fails job once it has used MaxAttempts, else requeues it
// after the exponential backoff retryDelayFor gives.
func (jq *JobQueue) handleJobFailure(job *Job, err error) error {
	job.Error = err.Error()

	if job.Attempts >= job.MaxAttempts {
		return jq.failJob(job, err)
	}

	job.Status = JobStatusRetrying
	job.ScheduledAt = time.Now().Add(jq.retryDelayFor(job.Attempts))

	if err := jq.storeJob(job); err != nil {
		return errors.Join(err, jq.requeueJob(job))
	}

	return errors.Join(jq.removeFromProcessingQueue(job), jq.requeueJob(job))
}

// retryDelayFor returns the wait after a job's attempts-th failed attempt:
// RetryDelay doubled for each attempt after the first, capped at MaxRetryDelay.
func (jq *JobQueue) retryDelayFor(attempts int) time.Duration {
	delay := jq.retryDelay
	for i := 1; i < attempts && delay < jq.maxRetryDelay; i++ {
		delay *= 2
	}
	return min(delay, jq.maxRetryDelay)
}

// failJob marks job permanently failed. It is not retried again.
func (jq *JobQueue) failJob(job *Job, err error) error {
	now := time.Now()
	job.Status = JobStatusFailed
	job.FailedAt = &now
	job.Error = err.Error()

	if err := jq.storeJob(job); err != nil {
		return err
	}

	log.Printf("Job %s permanently failed after %d attempts: %v", job.ID, job.Attempts, err)
	return jq.removeFromProcessingQueue(job)
}

// requeueJob adds job back to the pending queue.
func (jq *JobQueue) requeueJob(job *Job) error {
	queueKey := jq.getQueueKey(JobStatusPending)
	jobRef := JobReference{
		ID:          job.ID,
		Priority:    job.Priority,
		ScheduledAt: job.ScheduledAt,
	}

	return jq.addToQueue(queueKey, jobRef)
}

// addToQueue stores jobRef under queueKey and the job's ID.
func (jq *JobQueue) addToQueue(queueKey string, jobRef JobReference) error {
	refKey := fmt.Sprintf("%s:%s", queueKey, jobRef.ID)
	data, err := json.Marshal(jobRef)
	if err != nil {
		return err
	}

	return jq.store.Set(refKey, data)
}

// addToProcessingQueue records job as in progress, expiring after
// ProcessingTTL on stores that support TTLs.
func (jq *JobQueue) addToProcessingQueue(job *Job) error {
	processingKey := jq.getProcessingKey(job.ID)
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}

	if ttlStore, ok := jq.store.(TTLStore); ok {
		return ttlStore.SetTTL(processingKey, data, jq.processingTTL)
	}

	return jq.store.Set(processingKey, data)
}

// removeFromProcessingQueue deletes the in-progress record for job.
func (jq *JobQueue) removeFromProcessingQueue(job *Job) error {
	if err := jq.store.Delete(jq.getProcessingKey(job.ID)); err != nil {
		return fmt.Errorf("remove in-progress record for job %s: %w", job.ID, err)
	}
	return nil
}

// storeJobResult saves result, for 24 hours on stores that support TTLs.
func (jq *JobQueue) storeJobResult(result *JobResult) error {
	resultKey := jq.getResultKey(result.JobID)
	data, err := json.Marshal(result)
	if err != nil {
		return err
	}

	if ttlStore, ok := jq.store.(TTLStore); ok {
		return ttlStore.SetTTL(resultKey, data, 24*time.Hour)
	}

	return jq.store.Set(resultKey, data)
}

// GetJob returns the job stored under jobID, or the store's not-found error.
func (jq *JobQueue) GetJob(jobID string) (*Job, error) {
	return jq.getJob(jobID)
}

// GetJobResult returns the result of a completed job. Failed jobs have none;
// read Job.Error instead.
func (jq *JobQueue) GetJobResult(jobID string) (*JobResult, error) {
	resultKey := jq.getResultKey(jobID)
	data, err := jq.store.Get(resultKey)
	if err != nil {
		return nil, err
	}

	var result JobResult
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// GetStats counts the stored jobs by status. It reads every job record, so
// it costs one store read per job ever pushed: completed and failed jobs stay
// stored until deleted. A job whose worker crashed stays "processing".
func (jq *JobQueue) GetStats() (*JobQueueStats, error) {
	prefix := jq.getJobKey("")
	keys, err := jq.store.Keys(prefix)
	if err != nil {
		return nil, err
	}

	stats := &JobQueueStats{Workers: jq.workers}
	for _, key := range keys {
		data, err := jq.store.Get(key)
		if errors.Is(err, hyperchi.ErrKeyNotFound) {
			continue // deleted since Keys listed it
		}
		if err != nil {
			return nil, err
		}
		var job Job
		if err := json.Unmarshal(data, &job); err != nil {
			return nil, fmt.Errorf("job record %q: %w", strings.TrimPrefix(key, prefix), err)
		}
		stats.TotalJobs++
		switch job.Status {
		case JobStatusPending:
			stats.PendingJobs++
		case JobStatusRetrying:
			stats.RetryingJobs++
		case JobStatusProcessing:
			stats.ProcessingJobs++
		case JobStatusCompleted:
			stats.CompletedJobs++
		case JobStatusFailed:
			stats.FailedJobs++
		}
	}

	jq.mu.RLock()
	for jobType := range jq.handlers {
		stats.Handlers = append(stats.Handlers, jobType)
	}
	jq.mu.RUnlock()
	slices.Sort(stats.Handlers)

	return stats, nil
}

// Store keys, all under keyPrefix.
func (jq *JobQueue) getJobKey(jobID string) string {
	return fmt.Sprintf("%sjob:%s", jq.keyPrefix, jobID)
}

func (jq *JobQueue) getQueueKey(status JobStatus) string {
	return fmt.Sprintf("%squeue:%s", jq.keyPrefix, status)
}

func (jq *JobQueue) getProcessingKey(jobID string) string {
	return fmt.Sprintf("%sprocessing:%s", jq.keyPrefix, jobID)
}

func (jq *JobQueue) getResultKey(jobID string) string {
	return fmt.Sprintf("%sresult:%s", jq.keyPrefix, jobID)
}

// JobQueueStats is what GetStats reports: stored jobs by status, the worker
// count, and the job types with a registered handler, sorted.
type JobQueueStats struct {
	TotalJobs      int      `json:"total_jobs"`
	PendingJobs    int      `json:"pending_jobs"`
	RetryingJobs   int      `json:"retrying_jobs"`
	ProcessingJobs int      `json:"processing_jobs"`
	CompletedJobs  int      `json:"completed_jobs"`
	FailedJobs     int      `json:"failed_jobs"`
	Workers        int      `json:"workers"`
	Handlers       []string `json:"handlers"`
}
