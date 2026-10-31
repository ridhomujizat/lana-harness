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
		fmt.Fprintln(fs.Output(), "Usage: lana-eval [flags]")
		fmt.Fprintln(fs.Output())
		fmt.Fprintln(fs.Output(), "Runs the fixed developer eval tasks against a real provider.")
		fmt.Fprintln(fs.Output())
		fmt.Fprintln(fs.Output(), "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 1
	}

	var filter *regexp.Regexp
	if *runPattern != "" {
		compiled, err := regexp.Compile(*runPattern)
		if err != nil {
			fmt.Fprintf(stderr, "error: invalid -run pattern: %v\n", err)
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
		fmt.Fprintln(stderr, "error: no eval tasks match -run")
		return 1
	}

	if os.Getenv(*keyEnv) == "" {
		fmt.Fprintf(stderr, "error: %s is empty; set it or choose another -key-env\n", *keyEnv)
		return 1
	}

	previous, err := loadPrevious(*comparePath)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	setFlags := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { setFlags[f.Name] = true })
	prices := priceTable{in: *priceIn, cached: *priceCached, out: *priceOut}
	prices.set = setFlags["price-in"] && setFlags["price-cached"] && setFlags["price-out"]
	if !prices.set && (setFlags["price-in"] || setFlags["price-cached"] || setFlags["price-out"]) {
		fmt.Fprintln(stderr, "warning: cost is disabled; set -price-in, -price-cached, and -price-out together")
	}

	document := report{
		Model:   *model,
		Started: time.Now().UTC(),
		Tasks:   make([]taskResult, 0, len(selected)),
	}
	cfg := runConfig{
		model:       *model,
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
			fmt.Fprintf(stderr, "error: %s: %v\n", task.Name, taskErr)
			return 1
		}
		document.Tasks = append(document.Tasks, result)
		fmt.Fprintf(stderr, "%s: %s\n", result.Name, result.Status)
	}
	document.Total = buildTotal(document.Tasks, prices)

	if *outPath != "" {
		data, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "error: encode report: %v\n", err)
			return 1
		}
		data = append(data, '\n')
		if err := os.WriteFile(*outPath, data, 0o644); err != nil {
			fmt.Fprintf(stderr, "error: write report: %v\n", err)
			return 1
		}
	}
	printTable(stdout, document.Tasks, document.Total, previous, prices.set)
	return 0
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
