package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/regiellis/hyperchi/hyperchi"
	"github.com/regiellis/hyperchi/hyperchi/database"
	"github.com/regiellis/hyperchi/hyperchi/extensions/sse"
	gc "github.com/regiellis/hyperchi/modules/gomponents"
)

// progressStream pushes send-out progress to open pages through the
// framework's SSE hub. A publish and a new subscriber's first snapshot both
// read the database under one lock, so a page never receives an older state
// after a newer one.
type progressStream struct {
	mu  sync.Mutex
	hub *sse.SSEHub
	db  *database.DB
}

func channel(id int64) string { return "sendout-" + strconv.FormatInt(id, 10) }

// Publish broadcasts the send-out's current progress panel. On the final
// publish it also sends the named "done" event, which closes the stream in
// the browser (hx-sse:close="done").
func (p *progressStream) Publish(ctx context.Context, id int64, final bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	html, err := p.panel(ctx, id)
	if err != nil {
		return err
	}
	p.hub.BroadcastSwap(channel(id), html)
	if final {
		p.hub.Broadcast(channel(id), sse.SSEMessage{Event: "done", Data: "done"})
	}
	return nil
}

func (p *progressStream) panel(ctx context.Context, id int64) (string, error) {
	pr, err := loadProgress(ctx, p.db, id)
	if err != nil {
		return "", err
	}
	html, err := gc.HTML(progressPanel(pr))
	return string(html), err
}

// subscribe adds an open stream to the send-out's channel and sends it the
// current state. It reports whether the job is still running.
func (p *progressStream) subscribe(ctx context.Context, connID string, id int64) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hub.Subscribe(connID, channel(id))
	pr, err := loadProgress(ctx, p.db, id)
	if err != nil {
		return false, err
	}
	html, err := gc.HTML(progressPanel(pr))
	if err != nil {
		return false, err
	}
	p.hub.SendToConnection(connID, sse.SSEMessage{Data: string(html)})
	return pr.Running(), nil
}

func (s *Server) sendoutsPage(c *hyperchi.Context) error {
	rows, err := database.ListStruct[Sendout](s.db, sendoutSelect+` ORDER BY s.id DESC`)
	if err != nil {
		return err
	}
	progress := make([]Progress, 0, len(rows))
	for _, so := range rows {
		p, err := loadProgress(c.Ctx(), s.db, so.ID)
		if err != nil {
			return err
		}
		progress = append(progress, p)
	}
	return s.page(c, "Send-outs", "sendouts", sendoutsView(progress))
}

// SendoutPlan is what starting a send-out will do, shown before it starts.
type SendoutPlan struct {
	Letters    []Letter
	Lists      []List
	Letter     *Letter
	List       *List
	Fields     []string
	Gaps       []FieldGap
	InOutbox   int
	ToRender   int
	Existing   *Sendout
	Running    bool
	CSRF       string
	Problem    string
	Confirming bool
}

func (s *Server) plan(c *hyperchi.Context, letterRaw, listRaw string) (SendoutPlan, error) {
	var p SendoutPlan
	var err error
	if p.Letters, err = allLetters(s.db); err != nil {
		return p, err
	}
	if p.Lists, err = allLists(s.db); err != nil {
		return p, err
	}
	p.CSRF = c.CSRFToken()
	letterID, lerr := strconv.ParseInt(letterRaw, 10, 64)
	listID, ierr := strconv.ParseInt(listRaw, 10, 64)
	if lerr != nil || ierr != nil {
		return p, nil
	}
	letter, err := getLetter(c.Ctx(), s.db, letterID)
	if err != nil {
		return p, notFound(err, "letter")
	}
	list, err := getList(s.db, listID)
	if err != nil {
		return p, notFound(err, "list")
	}
	p.Letter, p.List = &letter, &list
	p.Fields = mergeFields(letter.Subject, letter.Body)
	if p.Gaps, err = fieldGaps(s.db, listID, p.Fields); err != nil {
		return p, err
	}
	if p.InOutbox, err = s.db.Count("outbox", "letter_id = ? AND list_id = ? AND recipient_id IN (SELECT id FROM recipients WHERE list_id = ?)",
		letterID, listID, listID); err != nil {
		return p, err
	}
	p.ToRender = list.Recipients - p.InOutbox
	existing, err := one[Sendout](s.db, sendoutSelect+` WHERE s.letter_id = ? AND s.list_id = ?`, letterID, listID)
	switch {
	case err == nil:
		p.Existing = &existing
		p.Running = s.jobs.Running(existing.ID)
	case !errors.Is(err, errNotFound):
		return p, err
	}
	return p, nil
}

func (s *Server) newSendoutPage(c *hyperchi.Context) error {
	p, err := s.plan(c, c.Query("letter_id"), c.Query("list_id"))
	if err != nil {
		return err
	}
	if partial(c) {
		return render(c, sendoutPlanPanel(p))
	}
	return s.page(c, "New send-out", "sendouts", newSendoutView(p))
}

// StartSendout is the confirmation form.
type StartSendout struct {
	LetterID int64  `form:"letter_id" validate:"required"`
	ListID   int64  `form:"list_id" validate:"required"`
	Confirm  string `form:"confirm"`
}

// startSendout creates the send-out for this letter and list, or reuses the
// existing one, and starts its job. It refuses without the confirmation that
// letters go to the outbox and are not emailed.
func (s *Server) startSendout(c *hyperchi.Context) error {
	in, err := hyperchi.Bind[StartSendout](c)
	if err != nil {
		return err
	}
	p, err := s.plan(c, strconv.FormatInt(in.LetterID, 10), strconv.FormatInt(in.ListID, 10))
	if err != nil {
		return err
	}
	if in.Confirm != "outbox" {
		p.Problem = "Tick the box to confirm that letters go to the outbox and are not emailed."
		return render(c.Status(http.StatusUnprocessableEntity), sendoutPlanPanel(p))
	}
	if p.List.Recipients == 0 {
		p.Problem = "This list has no recipients yet. Import a CSV first."
		return render(c.Status(http.StatusUnprocessableEntity), sendoutPlanPanel(p))
	}
	var sid int64
	if p.Existing != nil {
		sid = p.Existing.ID
	} else {
		sid, err = s.db.Create("sendouts", map[string]any{
			"letter_id": in.LetterID, "list_id": in.ListID, "status": statusNew,
			"created_by": currentUser(c).ID, "created_at": time.Now().UTC(),
		})
		if err != nil {
			return err
		}
	}
	started, err := s.jobs.Start(sid)
	if err != nil {
		return err
	}
	if started {
		detail := fmt.Sprintf("send-out %d: letter %q to list %q (%d recipients, %d already in the outbox)",
			sid, p.Letter.Title, p.List.Name, p.List.Recipients, p.InOutbox)
		if err := audit(c.Ctx(), s.db, currentUser(c), "started a send-out", detail); err != nil {
			return err
		}
	}
	return c.Redirect(fmt.Sprintf("/sendouts/%d", sid))
}

func (s *Server) sendoutPage(c *hyperchi.Context) error {
	sid, err := id(c)
	if err != nil {
		return err
	}
	p, err := loadProgress(c.Ctx(), s.db, sid)
	if err != nil {
		return notFound(err, "send-out")
	}
	return s.page(c, "Send-out "+strconv.FormatInt(sid, 10), "sendouts", sendoutView(p, c.CSRFToken()))
}

func (s *Server) cancelSendout(c *hyperchi.Context) error {
	sid, err := id(c)
	if err != nil {
		return err
	}
	so, err := getSendout(c.Ctx(), s.db, sid)
	if err != nil {
		return notFound(err, "send-out")
	}
	if s.jobs.Cancel(sid) {
		if err := audit(c.Ctx(), s.db, currentUser(c), "cancelled a send-out",
			fmt.Sprintf("send-out %d: letter %q to list %q", sid, so.LetterTitle, so.ListName)); err != nil {
			return err
		}
	}
	// The job publishes the final state to every open page; this request
	// needs no body.
	return c.NoContent()
}

// sendoutEvents streams a send-out's progress as Server-Sent Events. The
// first message is the current state from the database, so a reloaded page
// is up to date at once; the job's updates follow. The handler owns the
// response for as long as the browser keeps it open.
func (s *Server) sendoutEvents(c *hyperchi.Context) error {
	sid, err := id(c)
	if err != nil {
		return err
	}
	if _, err := getSendout(c.Ctx(), s.db, sid); err != nil {
		return notFound(err, "send-out")
	}
	// The server's read and write timeouts are sized for ordinary requests.
	// Clear them for this response, as net/http advises for streams. The hub
	// does this in its own HTTPHandler; this route uses the connection API
	// instead, so it clears them itself. The hub's sweep pings every open
	// stream every 30 seconds, which keeps a quiet stream alive.
	rc := http.NewResponseController(c.Response())
	for _, set := range []func(time.Time) error{rc.SetReadDeadline, rc.SetWriteDeadline} {
		if err := set(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
			return err
		}
	}
	c.SetHeader("Content-Type", "text/event-stream").
		SetHeader("Cache-Control", "no-cache").
		SetHeader("X-Accel-Buffering", "no")
	connID, err := newConnID()
	if err != nil {
		return err
	}
	conn := sse.NewSSEConnection(connID, c.Response(), c.Request())
	if conn.Flusher == nil {
		return errors.New("event stream: the response cannot be flushed")
	}
	s.stream.hub.AddConnection(conn)
	defer s.stream.hub.RemoveConnection(connID)
	running, err := s.stream.subscribe(c.Ctx(), connID, sid)
	if err != nil {
		return err
	}
	if !running {
		s.stream.hub.SendToConnection(connID, sse.SSEMessage{Event: "done", Data: "done"})
		return nil
	}
	select {
	case <-conn.Context.Done():
	case <-s.opts.Shutdown.Done():
	}
	return nil
}

func newConnID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "mailgrid_" + hex.EncodeToString(b[:]), nil
}

// statusLabel is the wording people see for a send-out status.
func statusLabel(status string) string {
	switch status {
	case statusNew:
		return "Not started"
	case statusRunning:
		return "Rendering"
	case statusDone:
		return "Finished"
	case statusCancelled:
		return "Cancelled"
	case statusInterrupted:
		return "Interrupted"
	case statusFailed:
		return "Failed"
	case "rendered":
		return "Rendered"
	case "queued":
		return "Queued"
	case "":
		return "Unknown"
	}
	return strings.ToUpper(status[:1]) + status[1:]
}
