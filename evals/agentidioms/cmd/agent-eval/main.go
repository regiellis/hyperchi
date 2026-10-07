// Command agent-eval measures whether coding agents write HyperChi's API or
// plain net/http. For each task in the tasks directory it scaffolds a fresh
// project with the hyperchi CLI, runs a headless agent in it with the task as
// the prompt, and grades the result with the agentidioms package.
//
//	go run ./evals/agentidioms/cmd/agent-eval -model sonnet
//
// The agent runs as `claude -p` with the project as its working directory, so
// it reads the project's CLAUDE.md and AGENTS.md like a real session. It bills
// to whatever credentials claude uses; unset ANTHROPIC_API_KEY to use a
// subscription login. Each run writes to a new timestamped directory.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/regiellis/hyperchi/evals/agentidioms"
)

const promptSuffix = `

Work only inside this project directory. Use whatever the project's own
instructions recommend. When you are done, ` + "`go vet ./...`" + ` must pass. Do not
leave any server running.`

type result struct {
	Task      string             `json:"task"`
	Project   string             `json:"project"`
	Seconds   float64            `json:"seconds"`
	AgentErr  string             `json:"agent_error,omitempty"`
	Report    agentidioms.Report `json:"report"`
	Pass      bool               `json:"pass"`
	Timestamp string             `json:"timestamp"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	framework := flag.String("framework", ".", "HyperChi checkout the scaffolded projects build against")
	out := flag.String("out", "tmp/agent-eval", "directory for run output (a timestamped subdirectory per run)")
	tasks := flag.String("tasks", "evals/agentidioms/tasks", "directory of task prompts (*.md)")
	model := flag.String("model", "sonnet", "model passed to the agent")
	agent := flag.String("agent", "claude", "agent command; run as <agent> -p <prompt> --model <model> ...")
	only := flag.String("only", "", "comma-separated task names to run (default: all)")
	parallel := flag.Int("parallel", 4, "tasks to run at once")
	timeout := flag.Duration("timeout", 20*time.Minute, "per-task agent timeout")
	gradeOnly := flag.String("grade", "", "grade an existing project directory and exit")
	noGuide := flag.Bool("no-guide", false, "control run: delete the scaffold's AGENTS.md and CLAUDE.md before the agent starts")
	flag.Parse()

	if *gradeOnly != "" {
		rep, err := agentidioms.Grade(*gradeOnly)
		if err != nil {
			return err
		}
		agentidioms.Build(&rep)
		fmt.Print(agentidioms.Summary(map[string]agentidioms.Report{filepath.Base(*gradeOnly): rep}))
		if rep.BuildError != "" {
			fmt.Println(rep.BuildError)
		}
		return nil
	}

	root, err := filepath.Abs(*framework)
	if err != nil {
		return err
	}
	runDir, err := filepath.Abs(filepath.Join(*out, time.Now().Format("20060102-150405")))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	cli := filepath.Join(runDir, "hyperchi")
	// The CLI is its own module under cmd/hyperchi; its replace directive builds
	// it against this checkout.
	build := exec.Command("go", "build", "-ldflags", "-X main.frameworkBuildPath="+root, "-o", cli, ".")
	build.Dir = filepath.Join(root, "cmd", "hyperchi")
	if b, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build hyperchi CLI: %v\n%s", err, b)
	}

	names, err := taskNames(*tasks, *only)
	if err != nil {
		return err
	}
	results := make([]result, len(names))
	sem := make(chan struct{}, max(*parallel, 1))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = runTask(cli, runDir, *tasks, name, *agent, *model, *timeout, *noGuide)
			log.Printf("%s: pass=%v builds=%v (%.0fs)", name, results[i].Pass, results[i].Report.Builds, results[i].Seconds)
		}(i, name)
	}
	wg.Wait()

	rows := map[string]agentidioms.Report{}
	for _, r := range results {
		rows[r.Task] = r.Report
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return err
	}
	if err := agentidioms.WriteFile(filepath.Join(runDir, "results.json"), string(data)+"\n"); err != nil {
		return err
	}
	variant := "with AGENTS.md"
	if *noGuide {
		variant = "control, no AGENTS.md"
	}
	summary := fmt.Sprintf("# Agent idiom eval, %s, model %s, %s\n\n%s", filepath.Base(runDir), *model, variant, agentidioms.Summary(rows))
	if err := agentidioms.WriteFile(filepath.Join(runDir, "summary.md"), summary); err != nil {
		return err
	}
	fmt.Print(summary)
	fmt.Printf("\nOutput: %s\n", runDir)
	return nil
}

func taskNames(dir, only string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for n := range strings.SplitSeq(only, ",") {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	var names []string
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".md")
		if len(want) == 0 || want[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, errors.New("no tasks matched")
	}
	return names, nil
}

func runTask(cli, runDir, tasksDir, name, agent, model string, timeout time.Duration, noGuide bool) result {
	res := result{Task: name, Timestamp: time.Now().Format(time.RFC3339)}
	taskDir := filepath.Join(runDir, name)
	project := filepath.Join(taskDir, "app")
	res.Project = project
	res.Report = agentidioms.Report{Dir: project}
	fail := func(err error) result {
		res.AgentErr = err.Error()
		return res
	}
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return fail(err)
	}
	scaffold := exec.Command(cli, "new", "app")
	scaffold.Dir = taskDir
	if b, err := scaffold.CombinedOutput(); err != nil {
		return fail(fmt.Errorf("scaffold: %v\n%s", err, b))
	}
	if noGuide {
		for _, f := range []string{"AGENTS.md", "CLAUDE.md"} {
			if err := os.Remove(filepath.Join(project, f)); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fail(err)
			}
		}
	}
	prompt, err := os.ReadFile(filepath.Join(tasksDir, name+".md"))
	if err != nil {
		return fail(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(ctx, agent, "-p", strings.TrimSpace(string(prompt))+promptSuffix,
		"--model", model, "--output-format", "json", "--dangerously-skip-permissions")
	cmd.Dir = project
	transcript, terr := os.Create(filepath.Join(taskDir, "agent-output.json"))
	if terr != nil {
		return fail(terr)
	}
	defer func() {
		if err := transcript.Close(); err != nil {
			log.Printf("%s: close transcript: %v", name, err)
		}
	}()
	stderr, serr := os.Create(filepath.Join(taskDir, "agent-stderr.log"))
	if serr != nil {
		return fail(serr)
	}
	defer func() {
		if err := stderr.Close(); err != nil {
			log.Printf("%s: close stderr log: %v", name, err)
		}
	}()
	cmd.Stdout, cmd.Stderr = transcript, stderr
	if err := cmd.Run(); err != nil {
		res.AgentErr = err.Error()
	}
	res.Seconds = time.Since(start).Seconds()

	rep, err := agentidioms.Grade(project)
	if err != nil {
		return fail(fmt.Errorf("grade: %w", err))
	}
	agentidioms.Build(&rep)
	res.Report = rep
	res.Pass = rep.Pass()
	return res
}
