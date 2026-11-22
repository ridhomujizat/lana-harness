package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
	"github.com/ridhomujizat/lana-harness/internal/workbench/gitinfo"
	"github.com/ridhomujizat/lana-harness/internal/workbench/preview"
	"github.com/ridhomujizat/lana-harness/web"
)

// Tunables of `lana workbench use` (spec 69, REQ-WA-020..023).
const (
	// workbenchUseTimeout bounds the report PUT and DELETE.
	workbenchUseTimeout = 10 * time.Second
	// workbenchUseCreateTimeout bounds the create POST, which may fetch origin.
	workbenchUseCreateTimeout = 60 * time.Second
	// workbenchUseGitTimeout bounds the local git lookups of `--new`.
	workbenchUseGitTimeout = 10 * time.Second
	// workbenchUseReportPath is the local report route (spec 69, API-WLG-008).
	workbenchUseReportPath = "/v1/local/workbench/sessions/"
	// workbenchUseCreatePath is the local create route (spec 66, spec 69).
	workbenchUseCreatePath = "/v1/local/workbench/git/worktrees"
	// workbenchUseAnswerMax caps the daemon answer the command reads.
	workbenchUseAnswerMax = 1 << 20
)

// The parts of `lana workbench use` tests replace: the pane check (REQ-WA-002),
// the daemon secret reader (API-WLG-008), the HTTP client and the local git
// runner.
var (
	workbenchUseInPane = workbench.InPane
	workbenchUseSecret = readDaemonSecret
	workbenchUseClient = &http.Client{}
	workbenchUseRunner = newWorkbenchUseRunner
)

// workbenchUseOptions is the parsed command line of `lana workbench use`.
type workbenchUseOptions struct {
	clear     bool
	newBranch string
	base      string
	noFetch   bool
	path      string
}

// workbenchUseCreateBody is the local create body (spec 66 api-contract). Its
// fields match the daemon route, which rejects unknown fields.
type workbenchUseCreateBody struct {
	Repo      string   `json:"repo"`
	Branch    string   `json:"branch"`
	NewBranch bool     `json:"new_branch"`
	Base      string   `json:"base"`
	Fetch     bool     `json:"fetch"`
	Path      string   `json:"path"`
	Copy      []string `json:"copy"`
}

// workbenchUseAnswer is the report route's answer (spec 69 api-contract): the
// resolved worktree, whose short and root the success line uses.
type workbenchUseAnswer struct {
	Worktree struct {
		Short    string `json:"short"`
		Worktree string `json:"worktree"`
	} `json:"worktree"`
}

// workbenchUseError is a non-success answer from the daemon, mapped to the
// message the agent sees (REQ-WA-023).
type workbenchUseError struct {
	status  int
	code    string
	message string
	path    string
}

// Error renders the failure as code and message.
func (e *workbenchUseError) Error() string {
	return "daemon " + e.code + ": " + e.message
}

// userText is the instruction the agent sees for the error code (REQ-WA-023).
func (e *workbenchUseError) userText() string {
	switch e.code {
	case "not_a_repo":
		return e.path + " is not inside a git repository"
	case "forbidden_path":
		return e.path + " is outside the folders the Workbench may read"
	case "workbench_unavailable":
		return "the Workbench is off"
	case "not_found":
		return "this pane's session is gone"
	case "branch_exists":
		return "the branch already exists"
	case "path_exists":
		return "the worktree folder already exists"
	}
	if strings.TrimSpace(e.message) != "" {
		return e.message
	}
	return fmt.Sprintf("the daemon returned status %d", e.status)
}

// cmdWorkbenchUse runs `lana workbench use ...` (REQ-WA-020..023): it reports
// the worktree that holds a path, clears the report, or creates a new worktree
// and reports it.
func cmdWorkbenchUse(args []string) int {
	id, ok := workbenchUseInPane()
	if !ok {
		fmt.Fprintln(os.Stderr, "not in a Lana Workbench pane")
		return 2
	}
	opts, code := parseWorkbenchUseArgs(args)
	if code != 0 {
		return code
	}
	home, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	stored, ok := workbenchUseSecret(home)
	if !ok {
		fmt.Fprintln(os.Stderr, "the Lana daemon is not running")
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail(err, 1)
	}

	switch {
	case opts.newBranch != "":
		return workbenchUseNew(stored, id, cwd, opts)
	case opts.clear:
		return workbenchUseClear(stored, id)
	}

	path, clear := workbenchUseDotTarget(opts.path, cwd)
	if clear {
		return workbenchUseClear(stored, id)
	}
	answer, err := workbenchUseReport(stored.Addr, stored.Secret, id, path)
	if err != nil {
		return workbenchUseFail(err)
	}
	workbenchUsePrint(answer)
	return 0
}

// parseWorkbenchUseArgs reads the command line of `lana workbench use`. A bad
// command line is a usage error with exit 2 (api-contract).
func parseWorkbenchUseArgs(args []string) (workbenchUseOptions, int) {
	var opts workbenchUseOptions
	var rest []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--clear":
			opts.clear = true
		case "--no-fetch":
			opts.noFetch = true
		case "--new":
			if i+1 >= len(args) {
				return opts, workbenchUseUsage("--new needs a branch")
			}
			i++
			opts.newBranch = args[i]
		case "--base":
			if i+1 >= len(args) {
				return opts, workbenchUseUsage("--base needs a branch")
			}
			i++
			opts.base = args[i]
		case "-h", "--help":
			return opts, workbenchUseUsage("")
		default:
			if strings.HasPrefix(a, "-") && a != "-" {
				return opts, workbenchUseUsage("unknown flag " + a)
			}
			rest = append(rest, a)
		}
	}
	switch {
	case opts.newBranch != "":
		if opts.clear || len(rest) > 0 {
			return opts, workbenchUseUsage("--new takes no path")
		}
	case opts.clear:
		if len(rest) > 0 {
			return opts, workbenchUseUsage("--clear takes no path")
		}
	default:
		if len(rest) != 1 {
			return opts, workbenchUseUsage("")
		}
		opts.path = rest[0]
	}
	return opts, 0
}

// workbenchUseUsage prints the usage line and returns exit 2.
func workbenchUseUsage(reason string) int {
	if reason != "" {
		fmt.Fprintf(os.Stderr, "error: %s\n", reason)
	}
	fmt.Fprintln(os.Stderr, "error: use lana workbench use <path>|.|--clear|--new <branch> [--base <branch>] [--no-fetch]")
	return 2
}

// workbenchUseDotTarget decides what a path argument means (REQ-WA-020,
// REQ-WA-021). Any path other than "." is reported as it is. For "." the pane's
// start folder would be the reference, but the hook only records it when the
// daemon sets LANA_WORKBENCH_DIR, so "back to the pane's own folder" cannot
// always be told apart from "report another worktree". The rule here: "." clears
// the report when the working directory and LANA_WORKBENCH_DIR are the same
// worktree, and without LANA_WORKBENCH_DIR "." is treated as --clear, which is
// the common case (Claude Code resets its shell to the launch folder).
func workbenchUseDotTarget(arg, cwd string) (path string, clear bool) {
	if arg != "." {
		if filepath.IsAbs(arg) {
			return filepath.Clean(arg), false
		}
		return filepath.Join(cwd, arg), false
	}
	start := os.Getenv("LANA_WORKBENCH_DIR")
	if start == "" {
		return "", true
	}
	cwdRoot, okCwd := workbenchUseRepoRoot(cwd)
	startRoot, okStart := workbenchUseRepoRoot(start)
	if okCwd && okStart && cwdRoot == startRoot {
		return "", true
	}
	return cwd, false
}

// workbenchUseNew creates a worktree and reports it (REQ-WA-022). The repo is
// the worktree root of the working directory; an existing branch is used as is
// and a new one is branched from the default base. A branch that already has a
// worktree is reported with its path so the agent can `use` it (REQ-WA-023).
func workbenchUseNew(stored daemonSecretFile, id, cwd string, opts workbenchUseOptions) int {
	repo, ok := workbenchUseRepoRoot(cwd)
	if !ok {
		fmt.Fprintln(os.Stderr, "not a git repository")
		return 1
	}
	runner := workbenchUseRunner()
	ctx, cancel := context.WithTimeout(context.Background(), workbenchUseCreateTimeout)
	defer cancel()

	list, err := runner.Worktrees(ctx, repo, "", "")
	if err != nil {
		return workbenchUseFail(err)
	}
	for _, wt := range list.Worktrees {
		if wt.Branch == opts.newBranch {
			fmt.Fprintf(os.Stderr, "branch %s already has a worktree at %s; run `lana workbench use %s`\n",
				opts.newBranch, wt.Path, wt.Path)
			return 1
		}
	}

	folder, err := runner.SuggestPath(ctx, repo, opts.newBranch, "")
	if err != nil {
		return workbenchUseFail(err)
	}
	base := opts.base
	if base == "" {
		base = runner.DefaultSettings(ctx, repo).Base
	}
	copyList, err := workbenchUseDefaultCopy(ctx, runner, repo)
	if err != nil {
		return workbenchUseFail(err)
	}

	body := workbenchUseCreateBody{
		Repo:      repo,
		Branch:    opts.newBranch,
		NewBranch: !workbenchUseBranchExists(ctx, runner, repo, opts.newBranch),
		Base:      base,
		Fetch:     !opts.noFetch,
		Path:      folder,
		Copy:      copyList,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return fail(err, 1)
	}
	status, data, err := workbenchUseDo(ctx, http.MethodPost,
		workbenchUseBase(stored.Addr)+workbenchUseCreatePath, stored.Secret, payload)
	if err != nil {
		return workbenchUseFail(err)
	}
	if status != http.StatusCreated {
		return workbenchUseFail(workbenchUseAnswerError(status, data, folder))
	}
	var created struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &created); err != nil || created.Path == "" {
		created.Path = folder
	}

	answer, err := workbenchUseReport(stored.Addr, stored.Secret, id, created.Path)
	if err != nil {
		return workbenchUseFail(err)
	}
	workbenchUsePrint(answer)
	fmt.Fprintf(os.Stdout, "Created %s\n", created.Path)
	return 0
}

// workbenchUseDefaultCopy is the worktree-relative paths the create copies by
// default (REQ-WG-032), checked by default in the new-worktree dialog.
func workbenchUseDefaultCopy(ctx context.Context, runner *gitinfo.Runner, repo string) ([]string, error) {
	candidates, err := runner.CopyCandidates(ctx, repo)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range candidates {
		if c.Default {
			out = append(out, c.Path)
		}
	}
	return out, nil
}

// workbenchUseBranchExists reports whether a branch exists locally or as
// origin/<branch>, so an existing branch is checked out instead of made
// (REQ-WA-022).
func workbenchUseBranchExists(ctx context.Context, runner *gitinfo.Runner, repo, branch string) bool {
	if _, err := runner.Git(ctx, repo, "rev-parse", "--verify", "-q", "refs/heads/"+branch); err == nil {
		return true
	}
	_, err := runner.Git(ctx, repo, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+branch)
	return err == nil
}

// workbenchUseRepoRoot is the worktree root of dir, or false when dir is not
// inside a git repository (REQ-WA-022).
func workbenchUseRepoRoot(dir string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), workbenchUseGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel")
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", false
	}
	return root, true
}

// workbenchUseReport PUTs a path report and returns the daemon's answer
// (REQ-WA-020).
func workbenchUseReport(addr, secret, id, path string) (workbenchUseAnswer, error) {
	body, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		return workbenchUseAnswer{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), workbenchUseTimeout)
	defer cancel()
	status, data, err := workbenchUseDo(ctx, http.MethodPut, workbenchUseReportURL(addr, id), secret, body)
	if err != nil {
		return workbenchUseAnswer{}, err
	}
	if status != http.StatusOK {
		return workbenchUseAnswer{}, workbenchUseAnswerError(status, data, path)
	}
	var answer workbenchUseAnswer
	if err := json.Unmarshal(data, &answer); err != nil {
		return workbenchUseAnswer{}, errors.New("the daemon returned an invalid answer")
	}
	return answer, nil
}

// workbenchUseClear removes the session's report (REQ-WA-021).
func workbenchUseClear(stored daemonSecretFile, id string) int {
	ctx, cancel := context.WithTimeout(context.Background(), workbenchUseTimeout)
	defer cancel()
	status, data, err := workbenchUseDo(ctx, http.MethodDelete, workbenchUseReportURL(stored.Addr, id), stored.Secret, nil)
	if err != nil {
		return workbenchUseFail(err)
	}
	if status != http.StatusNoContent && status != http.StatusOK {
		return workbenchUseFail(workbenchUseAnswerError(status, data, ""))
	}
	fmt.Fprintln(os.Stdout, "Workbench: following the pane's own folder again")
	return 0
}

// workbenchUseDo runs one daemon request with the local secret and returns the
// status and body (API-WLG-008).
func workbenchUseDo(ctx context.Context, method, target, secret string, body []byte) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set(web.LocalSecretHeader, secret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := workbenchUseClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, workbenchUseAnswerMax))
	return resp.StatusCode, data, nil
}

// workbenchUseBase is the daemon base URL of an address from daemon.secret.
func workbenchUseBase(addr string) string {
	return "http://" + addr
}

// workbenchUseReportURL is the local report route for a session (spec 69).
func workbenchUseReportURL(addr, id string) string {
	return workbenchUseBase(addr) + workbenchUseReportPath + url.PathEscape(id) + "/worktree"
}

// workbenchUseAnswerError maps a non-success daemon answer to the error the
// agent sees (REQ-WA-023).
func workbenchUseAnswerError(status int, data []byte, path string) error {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(data, &env)
	return &workbenchUseError{status: status, code: env.Error.Code, message: env.Error.Message, path: path}
}

// workbenchUsePrint writes the success line of a report (spec 69 api-contract):
// one line, the short branch and the worktree root.
func workbenchUsePrint(answer workbenchUseAnswer) {
	fmt.Fprintf(os.Stdout, "Workbench: %s (%s)\n", answer.Worktree.Short, answer.Worktree.Worktree)
}

// workbenchUseFail prints err to stderr and returns exit 1 (REQ-WA-023): a
// refused connection means the daemon is not running.
func workbenchUseFail(err error) int {
	var de *workbenchUseError
	switch {
	case errors.As(err, &de):
		fmt.Fprintln(os.Stderr, de.userText())
	case errors.Is(err, syscall.ECONNREFUSED):
		fmt.Fprintln(os.Stderr, "the Lana daemon is not running")
	default:
		fmt.Fprintln(os.Stderr, err)
	}
	return 1
}

// newWorkbenchUseRunner builds the local git runner `use --new` computes the
// folder, the default base and the copy candidates with (REQ-WA-022). Its path
// policy is the daemon's spec 61 policy: the user's home and the temp folders.
func newWorkbenchUseRunner() *gitinfo.Runner {
	home, _ := os.UserHomeDir()
	temps := []string{"/tmp", "/private/tmp"}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		temps = append(temps, tmp)
	}
	return gitinfo.NewRunner(preview.NewPolicy(home, temps))
}
