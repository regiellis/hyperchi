# Agent idiom eval results

## 2026-10-07, Claude Sonnet, four tasks, one run, v0.3.0

Run after the agent guide gained the CSRF rule (`AutoMiddleware` now refuses unsafe requests
without the token).

| Run | 1 signup form | 2 search fragment | 3 JSON API | 4 live chat | Pass rate |
| --- | --- | --- | --- | --- | --- |
| Scaffold with `AGENTS.md` | pass | pass | pass | pass | 4/4 |

Zero net/http fallbacks in every project; 33 to 108 seconds per task. The signup agent put
`<input type="hidden" name="csrf_token" value="{{.csrfToken}}">` in its form, as the guide
says. The grader checks code shape only, so the signup app was also run: a POST without the
token got 403, a bad email with the token 422 with the form re-rendered, and a valid signup
200. No control run this time; the October 5 control still describes what it can and cannot
show. Run directory: `tmp/agent-eval/20261007-143856`.

## 2026-10-05, Claude Sonnet, four tasks, one run each

| Run | 1 signup form | 2 search fragment | 3 JSON API | 4 live chat | Pass rate |
| --- | --- | --- | --- | --- | --- |
| Scaffold with `AGENTS.md` | pass | pass | pass | pass | 4/4 |
| Control, `AGENTS.md` and `CLAUDE.md` deleted | pass | pass | pass | pass | 4/4 |

Every project built with zero net/http fallbacks. Both runs used Context handlers throughout,
`hyperchi.Bind` with `FieldErrors` and a 422 fragment for the signup, `c.JSON` with
`hyperchi.NotFound`/`BadRequest` for the API, and `app.WebSocket` with `BindMessage`,
`s.Swap`, and `app.Room` for the chat. Agents took 31 to 108 seconds per task. The outputs
were read, not just graded: they are complete, idiomatic implementations.

**What the control does and does not show.** The control agents still found the framework's
guidance: the scaffolded `go.mod` replaces HyperChi with the local checkout, whose README,
package docs, examples, and `cmd/_scaffold/AGENTS.md` all teach the Context API (one control
agent says it followed "the scaffold's AGENTS.md" after reading it from the framework
source). So the control does not isolate the in-project guide. It shows that the framework's
own code and docs now steer agents correctly even without it, which is close to what a
module downloaded from the proxy would offer. Isolating `AGENTS.md` would need a framework
copy stripped of docs; not worth building while both variants pass.

Before the context-first API, the scaffold itself used `(w, r)` handlers rendering through
JSON, so a pass rate was not measurable in the same terms; that is the regression this eval
now guards. Run directories: `tmp/agent-eval/20261005-125740` (with guide) and
`tmp/agent-eval/20261005-125913` (control).
