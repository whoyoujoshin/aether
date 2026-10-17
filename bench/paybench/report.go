package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
)

// report prints the results page (Markdown) for runs: a summary per model,
// then every run with a column per task.
func report(paths []string) {
	var runs []Run
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			log.Fatal(err)
		}
		var r Run
		if err := json.Unmarshal(raw, &r); err != nil {
			log.Fatalf("%s: %v", p, err)
		}
		runs = append(runs, r)
	}
	fmt.Print(renderReport(runs))
}

// notMeasured says a task tells nothing about the agent: a tool call failed
// on the way to or from the test wallet (not as a tool error), or the
// connection to the model's API broke, and the task wasn't completed. Such
// tasks are left out of the scores.
func notMeasured(res Result) bool {
	if res.Passed {
		return false
	}
	if modelCut(res.Error) {
		return true
	}
	for _, c := range res.Calls {
		if c.IsError && strings.HasPrefix(c.Result, transportFailure) {
			return true
		}
	}
	return false
}

// modelCut is a request to the model's API that never got an answer: the
// HTTP client reports it as `Post "<url>": <cause>`. A task running out of
// time ends the same way, but that is the agent being slow, so it counts.
func modelCut(err string) bool {
	return strings.HasPrefix(err, `Post "`) &&
		!strings.Contains(err, "context deadline exceeded") && !strings.Contains(err, "context canceled")
}

// tally is a run's (or a model's) points over the tasks that were measured.
type tally struct {
	points            float64
	measured, skipped int
	passed            int
}

func (t *tally) add(res Result) {
	if notMeasured(res) {
		t.skipped++
		return
	}
	t.measured++
	t.points += res.Points
	if res.Passed {
		t.passed++
	}
}

func (t tally) share() float64 {
	if t.measured == 0 {
		return 0
	}
	return t.points / float64(t.measured)
}

func renderReport(runs []Run) string {
	var tasks []string
	seen := map[string]bool{}
	first, last := "", ""
	endpoints := map[string]bool{}
	versions := map[string]bool{}
	for _, r := range runs {
		day := r.Started.Format("2006-01-02")
		if first == "" || day < first {
			first = day
		}
		if day > last {
			last = day
		}
		endpoints[r.Endpoint] = true
		versions[r.TasksVersion] = true
		for _, res := range r.Results {
			if !seen[res.Task] {
				seen[res.Task] = true
				tasks = append(tasks, res.Task)
			}
		}
	}

	// Per model (by label, and by task-set version when runs span more
	// than one: their scores aren't comparable), best first.
	key := func(r Run) string {
		if len(versions) > 1 {
			return r.Label + " (v" + r.TasksVersion + ")"
		}
		return r.Label
	}
	byModel := map[string]*tally{}
	count := map[string]int{}
	var models []string
	for _, r := range runs {
		if byModel[key(r)] == nil {
			byModel[key(r)] = &tally{}
			models = append(models, key(r))
		}
		count[key(r)]++
		for _, res := range r.Results {
			byModel[key(r)].add(res)
		}
	}
	sort.SliceStable(models, func(i, j int) bool {
		a, b := byModel[models[i]], byModel[models[j]]
		if a.share() != b.share() {
			return a.share() > b.share()
		}
		return models[i] < models[j]
	})
	rank := map[string]int{}
	for i, m := range models {
		rank[m] = i
	}
	sort.SliceStable(runs, func(i, j int) bool {
		if key(runs[i]) != key(runs[j]) {
			return rank[key(runs[i])] < rank[key(runs[j])]
		}
		return runs[i].Started.Before(runs[j].Started)
	})

	var b strings.Builder
	b.WriteString("# Payments benchmark results\n\n")
	when := first
	if last != first {
		when = first + " to " + last
	}
	fmt.Fprintf(&b, "Task set version %s · runs on %s · test wallet %s\n\n", joinKeys(versions), when, joinKeys(endpoints))

	b.WriteString("## By model\n\n| Model | Runs | Score | Tasks passed |\n|---|---|---|---|\n")
	anySkipped := false
	for _, m := range models {
		t := byModel[m]
		passed := fmt.Sprintf("%d of %d", t.passed, t.measured)
		if t.skipped > 0 {
			anySkipped = true
			passed += fmt.Sprintf(" (%d not measured)", t.skipped)
		}
		fmt.Fprintf(&b, "| %s | %d | %.0f%% | %s |\n", m, count[m], 100*t.share(), passed)
	}

	b.WriteString("\n## Every run\n\n")
	fmt.Fprintf(&b, "| Model | Score | %s | Time | Tokens in/out | Spent |\n", strings.Join(tasks, " | "))
	fmt.Fprintf(&b, "|---|---|%s---|---|---|\n", strings.Repeat("---|", len(tasks)))
	for _, r := range runs {
		by := map[string]Result{}
		var t tally
		var secs float64
		var in, out, spent int64
		for _, res := range r.Results {
			by[res.Task] = res
			t.add(res)
			secs += res.Seconds
			in += res.InTokens
			out += res.OutTokens
			spent += res.Spent
		}
		cells := make([]string, len(tasks))
		for i, task := range tasks {
			res, ok := by[task]
			switch {
			case !ok:
				cells[i] = "–"
			case notMeasured(res):
				cells[i] = "n/m"
			case res.Passed:
				cells[i] = "✓"
			case res.Points > 0:
				cells[i] = fmt.Sprintf("%.2g", res.Points)
			default:
				cells[i] = "✗"
			}
		}
		fmt.Fprintf(&b, "| %s | %.2f / %d | %s | %.0fs | %s / %s | %d uaeth |\n",
			key(r), t.points, t.measured, strings.Join(cells, " | "), secs, kilo(in), kilo(out), spent)
	}
	b.WriteString("\n✓ full marks, ✗ none, a number for partial credit. Score is the points over the tasks measured; each task is worth 1. Time is the agents' own (not the harness's wallet setup or settling). Spent is what left the task wallets; fees are zero on the testnet.\n")
	if anySkipped {
		b.WriteString("\nn/m: not measured. The connection to the test wallet or to the model's API failed during the task and it wasn't completed, so it says nothing about the agent and is left out of the score. The run's file has the details.\n")
	}
	return b.String()
}

func joinKeys(m map[string]bool) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}

func kilo(n int64) string {
	if n >= 10000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprint(n)
}
