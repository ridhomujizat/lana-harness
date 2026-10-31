// Command lana-eval runs the developer eval tasks of REQ-TRL-020. It is a
// standalone developer command and is not wired into cmd/lana.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run parses flags, runs the selected tasks, writes the report, and returns the
// process exit code.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lana-eval", flag.ContinueOnError)
	fs.SetOutput(stderr)
	for _, arg := range args {
		if arg == "-h" || arg == "-help" || arg == "--help" {
			fs.SetOutput(stdout)
			break
		}
	}
	model := fs.String("model", "deepseek-flash", "model name")
	models := fs.String("models", "", "comma-separated model names (overrides -model)")
	providerURL := fs.String("provider-url", "https://api.deepseek.com/v1", "OpenAI-compatible provider base URL")
	keyEnv := fs.String("key-env", "DEEPSEEK_API_KEY", "environment variable that holds the provider API key")
	runPattern := fs.String("run", "", "only run tasks whose name matches this regular expression")
	outPath := fs.String("out", "", "write the JSON report to this file")
	comparePath := fs.String("compare", "", "compare against this earlier JSON report")
	timeout := fs.Duration("timeout", 10*time.Minute, "per-task timeout")
	keep := fs.Bool("keep", false, "keep the temporary home and workspace directories")
	priceIn := fs.Float64("price-in", 0, "USD per 1M uncached input tokens")
	priceCached := fs.Float64("price-cached", 0, "USD per 1M cached input tokens")
	priceOut := fs.Float64("price-out", 0, "USD per 1M output tokens")
	fs.Usage = func() {
		_, _ = fmt.Fprintln(fs.Output(), "Usage: lana-eval [flags]")
		_, _ = fmt.Fprintln(fs.Output())
		_, _ = fmt.Fprintln(fs.Output(), "Runs the fixed developer eval tasks against a real provider.")
		_, _ = fmt.Fprintln(fs.Output())
		_, _ = fmt.Fprintln(fs.Output(), "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}

	selectedModels, err := parseModels(*models, *model)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	var filter *regexp.Regexp
	if *runPattern != "" {
		compiled, err := regexp.Compile(*runPattern)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "error: invalid -run pattern: %v\n", err)
			return 1
		}
		filter = compiled
	}
	selected := make([]evalTask, 0, len(evalTasks))
	for _, task := range evalTasks {
		if filter == nil || filter.MatchString(task.Name) {
			selected = append(selected, task)
		}
	}
	if len(selected) == 0 {
		_, _ = fmt.Fprintln(stderr, "error: no eval tasks match -run")
		return 1
	}

	if os.Getenv(*keyEnv) == "" {
		_, _ = fmt.Fprintf(stderr, "error: %s is empty; set it or choose another -key-env\n", *keyEnv)
		return 1
	}

	previous, err := loadPrevious(*comparePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	setFlags := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })
	prices := priceTable{in: *priceIn, cached: *priceCached, out: *priceOut}
	prices.set = setFlags["price-in"] && setFlags["price-cached"] && setFlags["price-out"]
	if !prices.set && (setFlags["price-in"] || setFlags["price-cached"] || setFlags["price-out"]) {
		_, _ = fmt.Fprintln(stderr, "warning: cost is disabled; set -price-in, -price-cached, and -price-out together")
	}

	document := report{Started: time.Now().UTC()}
	runs := make([]modelReport, 0, len(selectedModels))
	for _, modelName := range selectedModels {
		run := modelReport{Model: modelName, Tasks: make([]taskResult, 0, len(selected))}
		cfg := runConfig{
			model:       modelName,
			providerURL: *providerURL,
			keyEnv:      *keyEnv,
			timeout:     *timeout,
			keep:        *keep,
			prices:      prices,
			stderr:      stderr,
		}
		for _, task := range selected {
			result, taskErr := runTask(context.Background(), task, cfg)
			if taskErr != nil {
				_, _ = fmt.Fprintf(stderr, "error: %s: %v\n", task.Name, taskErr)
				return 1
			}
			run.Tasks = append(run.Tasks, result)
			if len(selectedModels) > 1 {
				_, _ = fmt.Fprintf(stderr, "%s %s: %s\n", modelName, result.Name, result.Status)
			} else {
				_, _ = fmt.Fprintf(stderr, "%s: %s\n", result.Name, result.Status)
			}
		}
		run.Total = buildTotal(run.Tasks, prices)
		runs = append(runs, run)
	}

	if len(runs) == 1 {
		document.Model = runs[0].Model
		document.Tasks = runs[0].Tasks
		total := runs[0].Total
		document.Total = &total
	} else {
		document.Models = runs
	}

	if *outPath != "" {
		data, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "error: encode report: %v\n", err)
			return 1
		}
		data = append(data, '\n')
		if err := os.WriteFile(*outPath, data, 0o644); err != nil {
			_, _ = fmt.Fprintf(stderr, "error: write report: %v\n", err)
			return 1
		}
	}
	printReports(stdout, runs, previous, prices.set)
	return 0
}

// parseModels returns the models to run. A non-empty -models list overrides
// -model; duplicate names are dropped while order is kept.
func parseModels(list, fallback string) ([]string, error) {
	if strings.TrimSpace(list) == "" {
		if strings.TrimSpace(fallback) == "" {
			return nil, fmt.Errorf("-model is empty")
		}
		return []string{strings.TrimSpace(fallback)}, nil
	}
	seen := map[string]bool{}
	models := make([]string, 0, strings.Count(list, ",")+1)
	for _, part := range strings.Split(list, ",") {
		name := strings.TrimSpace(part)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		models = append(models, name)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("-models has no model names")
	}
	return models, nil
}

// loadPrevious reads the -compare report, if any.
func loadPrevious(path string) (*report, error) {
	if path == "" {
		return nil, nil
	}
	previous, err := loadReport(path)
	if err != nil {
		return nil, fmt.Errorf("load -compare report: %w", err)
	}
	return previous, nil
}
