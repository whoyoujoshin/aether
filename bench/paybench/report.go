package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
)

// report prints a Markdown table of runs: one row per run, a column per
// task, then the totals.
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

func renderReport(runs []Run) string {
	var tasks []string
	seen := map[string]bool{}
	for _, r := range runs {
		for _, res := range r.Results {
			if !seen[res.Task] {
				seen[res.Task] = true
				tasks = append(tasks, res.Task)
			}
		}
	}
	total := func(r Run) float64 {
		t := 0.0
		for _, res := range r.Results {
			t += res.Points
		}
		return t
	}
	sort.SliceStable(runs, func(i, j int) bool { return total(runs[i]) > total(runs[j]) })

	var b strings.Builder
	fmt.Fprintf(&b, "| Model | Score | %s | Time | Tokens in/out | Spent |\n", strings.Join(tasks, " | "))
	fmt.Fprintf(&b, "|---|---|%s---|---|---|\n", strings.Repeat("---|", len(tasks)))
	for _, r := range runs {
		by := map[string]Result{}
		var secs float64
		var in, out, spent int64
		for _, res := range r.Results {
			by[res.Task] = res
			secs += res.Seconds
			in += res.InTokens
			out += res.OutTokens
			spent += res.Spent
		}
		cells := make([]string, len(tasks))
		for i, t := range tasks {
			res, ok := by[t]
			switch {
			case !ok:
				cells[i] = "–"
			case res.Passed:
				cells[i] = "✓"
			case res.Points > 0:
				cells[i] = fmt.Sprintf("%.2g", res.Points)
			default:
				cells[i] = "✗"
			}
		}
		fmt.Fprintf(&b, "| %s | %.2f / %d | %s | %.0fs | %s / %s | %d uaeth |\n",
			r.Label, total(r), len(r.Results), strings.Join(cells, " | "), secs, kilo(in), kilo(out), spent)
	}
	b.WriteString("\n✓ full marks, ✗ none, a number for partial credit. Time is the agents' own (not the harness's wallet setup or settling). Spent is what left the task wallets; fees are zero on the testnet.\n")
	return b.String()
}

func kilo(n int64) string {
	if n >= 10000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprint(n)
}
