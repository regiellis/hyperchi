# Agent idiom eval

Measures whether a coding agent working in a HyperChi project writes the framework's API
(`func(c *hyperchi.Context) error`, `c.View`, `hyperchi.Bind`, `hyperchi.NotFound`, `c.HX()`,
`app.WebSocket`) or falls back to plain `net/http`. Scaffolded projects ship an `AGENTS.md`
for exactly this; the eval is how we know it works, and the gate that keeps it working.

## How it works

1. `cmd/agent-eval` builds the `hyperchi` CLI against this checkout and, for each task in
   `tasks/`, scaffolds a fresh project with `hyperchi new app`.
2. It runs a headless agent in the project: `claude -p <task> --model <model>`, with the
   project as the working directory, so the agent reads the project's `CLAUDE.md` and
   `AGENTS.md` like a real session. The task prompts never name HyperChi calls.
3. `Grade` parses the project's Go source and counts net/http fallbacks
   (`stdlib_handler`, `http_error`, `json_encoder`, `manual_hx_header`, `raw_query`,
   `parse_form`, `write_json`, `listen_and_serve`, `chi_new_router`) against framework
   calls; `go vet ./...` must pass. A task passes when it builds with zero fallbacks.
   `(w, r)` functions inside middleware are allowed and counted separately.

## Running it

```sh
task eval:agents                       # all tasks, Sonnet; spends model usage
task eval:agents MODEL=opus ONLY=4-live-chat
task eval:grade DIR=path/to/project    # grade an existing project, no agent
go test ./evals/...                    # the grader's own tests (no model usage)
```

Output lands in `tmp/agent-eval/<timestamp>/`: one directory per task with the project,
the agent's JSON output, and its stderr, plus `results.json` and `summary.md`. The agent
bills to whatever credentials `claude` uses; unset `ANTHROPIC_API_KEY` to use a
subscription login. Agent output varies between runs, so compare pass rates over the
task set, not single runs, and re-run a failing task before concluding anything.

## Changing it

Add a task as a new `tasks/<n>-<name>.md` written the way a person would ask, without
naming framework calls. Add a violation kind in `grade.go` with a case in
`grade_test.go`. Record each baseline run below.

## Results

Recorded runs and what they show: `RESULTS.md`.
