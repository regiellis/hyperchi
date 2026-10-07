package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/regiellis/hyperchi/hyperchi/database"
)

// publishFunc tells watchers a send-out changed. final is true once, when
// the job stops.
type publishFunc func(ctx context.Context, sendoutID int64, final bool) error

// Jobs runs send-outs in background goroutines, at most one per send-out.
// Each job renders recipients into the outbox one by one, so cancelling or
// a crash leaves the rest queued, and running the send-out again picks up
// where it stopped. Nothing here sends email.
type Jobs struct {
	base     context.Context
	db       *database.DB
	pace     time.Duration
	every    time.Duration
	publish  publishFunc
	mu       sync.Mutex
	running  map[int64]context.CancelFunc
	wg       sync.WaitGroup
	finished chan int64 // tests read job completions from here when set
}

func newJobs(base context.Context, db *database.DB, pace, every time.Duration, publish publishFunc) *Jobs {
	return &Jobs{base: base, db: db, pace: pace, every: every, publish: publish, running: map[int64]context.CancelFunc{}}
}

// Running reports whether the send-out's job is working in this process.
func (j *Jobs) Running(id int64) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	_, ok := j.running[id]
	return ok
}

// Start runs the send-out unless it is already running. It returns false
// when a job was already working on it.
func (j *Jobs) Start(id int64) (bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if _, ok := j.running[id]; ok {
		return false, nil
	}
	if err := j.base.Err(); err != nil {
		return false, fmt.Errorf("mailgrid is shutting down: %w", err)
	}
	if _, err := j.db.ExecContext(j.base, `UPDATE sendouts SET status = ?, error = '', current_email = '',
		started_at = ?, finished_at = NULL WHERE id = ?`, statusRunning, time.Now().UTC(), id); err != nil {
		return false, err
	}
	ctx, cancel := context.WithCancel(j.base)
	j.running[id] = cancel
	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		j.run(ctx, id)
		cancel()
		j.mu.Lock()
		delete(j.running, id)
		j.mu.Unlock()
		if j.finished != nil {
			j.finished <- id
		}
	}()
	return true, nil
}

// Cancel stops a running job after the recipient it is on. It reports
// whether there was a job to stop.
func (j *Jobs) Cancel(id int64) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	cancel, ok := j.running[id]
	if ok {
		cancel()
	}
	return ok
}

// Wait blocks until every job has stopped or ctx ends. The app calls it on
// shutdown, after the base context is cancelled, before closing the database.
func (j *Jobs) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		j.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("send-out jobs still running: %w", ctx.Err())
	}
}

// markInterrupted marks send-outs a previous process left running. Their
// queued recipients stay queued until someone runs the send-out again.
func (j *Jobs) markInterrupted(ctx context.Context) error {
	_, err := j.db.ExecContext(ctx, `UPDATE sendouts SET status = ?, error = ?, finished_at = ? WHERE status = ?`,
		statusInterrupted, "The server stopped while this send-out was running.", time.Now().UTC(), statusRunning)
	return err
}

// run queues every recipient not yet in the outbox for this letter and list,
// then renders the queue. The outbox has one row per (letter, list,
// recipient), which is what makes a rerun idempotent.
func (j *Jobs) run(ctx context.Context, id int64) {
	status, msg := statusDone, ""
	if err := j.render(ctx, id); err != nil {
		switch {
		case j.base.Err() != nil:
			status, msg = statusInterrupted, "The server stopped while this send-out was running."
		case errors.Is(err, context.Canceled) || ctx.Err() != nil:
			// A query the cancel interrupted fails with SQLite's own
			// "interrupted" error rather than context.Canceled.
			status = statusCancelled
		default:
			status, msg = statusFailed, err.Error()
			log.Printf("mailgrid: send-out %d failed: %v", id, err)
		}
	}
	// The job's context may be cancelled; the final write must still land.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if _, err := j.db.ExecContext(finish, `UPDATE sendouts SET status = ?, error = ?, current_email = '', finished_at = ?
		WHERE id = ?`, status, msg, time.Now().UTC(), id); err != nil {
		log.Printf("mailgrid: send-out %d: record final status: %v", id, err)
	}
	if err := j.publish(finish, id, true); err != nil {
		log.Printf("mailgrid: send-out %d: publish final progress: %v", id, err)
	}
}

func (j *Jobs) render(ctx context.Context, id int64) error {
	so, err := getSendout(ctx, j.db, id)
	if err != nil {
		return err
	}
	letter, err := getLetter(ctx, j.db, so.LetterID)
	if err != nil {
		return err
	}
	if _, err := j.db.ExecContext(ctx, `INSERT INTO outbox
		(sendout_id, letter_id, list_id, recipient_id, email, name, fields, status, format, created_at)
		SELECT ?, ?, r.list_id, r.id, r.email, r.name, r.fields, 'queued', ?, ?
		FROM recipients r WHERE r.list_id = ? ORDER BY r.id
		ON CONFLICT (letter_id, list_id, recipient_id) DO NOTHING`,
		id, so.LetterID, letter.Format, time.Now().UTC(), so.ListID); err != nil {
		return fmt.Errorf("queue recipients: %w", err)
	}
	if err := j.publish(ctx, id, false); err != nil {
		return err
	}
	last := time.Now()
	for {
		batch, err := database.ListStructContext[OutboxItem](ctx, j.db, `SELECT * FROM outbox
			WHERE sendout_id = ? AND status = 'queued' ORDER BY id LIMIT 100`, id)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, item := range batch {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := j.renderOne(ctx, letter, item); err != nil {
				return err
			}
			if j.every == 0 || time.Since(last) >= j.every {
				last = time.Now()
				if err := j.publish(ctx, id, false); err != nil {
					return err
				}
			}
			if err := j.wait(ctx); err != nil {
				return err
			}
		}
	}
}

// renderOne merges the letter for one queued row and stores the result. In
// a strict letter a recipient missing a merge field fails instead.
func (j *Jobs) renderOne(ctx context.Context, letter Letter, item OutboxItem) error {
	status, subject, body, problem := "rendered", "", "", ""
	values, err := decodeFields(item.FieldsJSON)
	if err != nil {
		status, problem = "failed", "The stored merge fields could not be read."
	} else {
		values["email"], values["name"] = item.Email, item.Name
		m := mergeLetter(letter.Subject, letter.Body, letter.Format, values)
		subject, body = m.Subject, m.Body
		switch {
		case letter.Strict && len(m.Missing) > 0:
			status, problem = "failed", "No value for "+strings.Join(braced(m.Missing), ", ")+"."
		case subject == "":
			status, problem = "failed", "The subject is empty for this recipient."
		}
	}
	_, err = j.db.ExecContext(ctx, `UPDATE outbox SET status = ?, subject = ?, body = ?, error = ?, rendered_at = ?
		WHERE id = ? AND status = 'queued'`, status, subject, body, problem, time.Now().UTC(), item.ID)
	if err != nil {
		return err
	}
	_, err = j.db.ExecContext(ctx, `UPDATE sendouts SET current_email = ? WHERE id = ?`, item.Email, item.SendoutID)
	return err
}

func (j *Jobs) wait(ctx context.Context) error {
	if j.pace <= 0 {
		return nil
	}
	t := time.NewTimer(j.pace)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func braced(fields []string) []string {
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = "{{" + f + "}}"
	}
	return out
}
