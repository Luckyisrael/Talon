package evals

import (
	"fmt"
	"sort"
	"strings"
)

// RenderScorecards formats a set of scorecards as a per-model report: one section
// per model, a row per eval with its verdict, score, and any failed checks, then a
// model summary line. Output is deterministic — models and evals are sorted — so a
// report can be diffed across runs to spot a regression.
func RenderScorecards(cards []Scorecard) string {
	byModel := map[string][]Scorecard{}
	for _, c := range cards {
		byModel[c.Model] = append(byModel[c.Model], c)
	}
	models := make([]string, 0, len(byModel))
	for m := range byModel {
		models = append(models, m)
	}
	sort.Strings(models)

	var b strings.Builder
	for i, model := range models {
		if i > 0 {
			b.WriteString("\n")
		}
		group := byModel[model]
		sort.Slice(group, func(i, j int) bool { return group[i].EvalID < group[j].EvalID })

		fmt.Fprintf(&b, "Model: %s\n", model)
		b.WriteString(strings.Repeat("─", 60) + "\n")

		passedEvals := 0
		var scoreSum float64
		for _, c := range group {
			verdict := "FAIL"
			if c.Passed() {
				verdict = "PASS"
				passedEvals++
			}
			scoreSum += c.Score()
			fmt.Fprintf(&b, "  %-18s %s  %3.0f%%", c.EvalID, verdict, c.Score()*100)
			if fails := c.Failures(); len(fails) > 0 {
				names := make([]string, len(fails))
				for i, f := range fails {
					names[i] = f.Name
					if f.Critical {
						names[i] += "!" // a bang marks a disqualifying failure
					}
				}
				fmt.Fprintf(&b, "  ✗ %s", strings.Join(names, ", "))
			}
			b.WriteString("\n")
		}

		mean := 0.0
		if len(group) > 0 {
			mean = scoreSum / float64(len(group))
		}
		fmt.Fprintf(&b, "  %-18s %d/%d evals passed, mean score %.0f%%\n",
			"summary", passedEvals, len(group), mean*100)
	}
	return b.String()
}
