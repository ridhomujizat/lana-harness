package toolbox

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sentinel errors of the toolbox (design "Jobs", "Command execution").
var (
	ErrNotFound       = errors.New("toolbox: not found")
	ErrExists         = errors.New("toolbox: already exists")
	ErrBusy           = errors.New("toolbox: a job is already running for this package")
	ErrInstalled      = errors.New("toolbox: package is installed")
	ErrNotInstalled   = errors.New("toolbox: package is not installed")
	ErrMethodMismatch = errors.New("toolbox: install method does not match this system")
	ErrCatalog        = errors.New("toolbox: built-in tools cannot be removed")
)

// catalogFS holds the built-in catalog of manifests shipped in the binary
// (REQ-TB-001).
//
//go:embed catalog/*.toml
var catalogFS embed.FS

// catalog is the built-in catalog: its manifests in id order and an index by
// id (REQ-TB-001).
type catalog struct {
	manifests []Manifest
	byID      map[string]Manifest
}

// loadCatalog parses the embedded catalog. A manifest that does not parse or
// validate is skipped with a log line, so a bad catalog file never breaks the
// daemon.
func loadCatalog(logger *slog.Logger) *catalog {
	c := &catalog{byID: map[string]Manifest{}}
	entries, err := fs.ReadDir(catalogFS, "catalog")
	if err != nil {
		logger.Error("toolbox: read catalog", "error", err)
		return c
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}
		data, err := catalogFS.ReadFile("catalog/" + entry.Name())
		if err != nil {
			logger.Error("toolbox: read catalog manifest", "file", entry.Name(), "error", err)
			continue
		}
		manifest, issues := ParseManifest(string(data))
		if len(issues) > 0 {
			logger.Error("toolbox: invalid catalog manifest", "file", entry.Name(), "issues", issuesText(issues))
			continue
		}
		c.manifests = append(c.manifests, manifest)
		c.byID[manifest.ID] = manifest
	}
	sort.Slice(c.manifests, func(i, j int) bool { return c.manifests[i].ID < c.manifests[j].ID })
	return c
}

// subscriberBuffer is the change-stream buffer of Subscribe (REQ-TB-002).
const subscriberBuffer = 32

// InvalidManifestError carries every manifest validation problem of a stored
// tool.toml.
type InvalidManifestError struct{ Issues []Issue }

// Error renders the issues as "<field>: <message>" joined by "; ".
func (e *InvalidManifestError) Error() string {
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		if issue.Field == "" {
			parts = append(parts, issue.Message)
			continue
		}
		parts = append(parts, issue.Field+": "+issue.Message)
	}
	return "invalid manifest: " + strings.Join(parts, "; ")
}

// Package is one package the Service reports (REQ-TB-001).
type Package struct {
	Manifest   Manifest
	Status     Status
	Installing bool
	// Problem is the parse error of a broken tool.toml, if any.
	Problem string
	// UpdatedAt is the tool.toml modification time in Unix milliseconds.
	UpdatedAt int64
	// Source is "catalog" for a built-in manifest and "custom" for one stored
	// on disk (REQ-TB-001).
	Source string
}

// Option configures a Service.
type Option func(*Service)

// WithSecrets installs the resolver of "secret:<NAME>" env values (REQ-TB-007).
func WithSecrets(secret func(string) (string, bool)) Option {
	return func(s *Service) { s.secrets = secret }
}

// WithLogger sets the Service logger (default slog.Default).
func WithLogger(l *slog.Logger) Option {
	return func(s *Service) {
		if l != nil {
			s.logger = l
		}
	}
}

// Service ties the file store, detection and the install jobs into every
// operation the web and the runtime call (spec #34). It is safe for concurrent
// use.
type Service struct {
	store   *Store
	jobs    *jobManager
	logger  *slog.Logger
	secrets func(string) (string, bool)
	catalog *catalog

	mu     sync.Mutex
	status map[string]Status

	subMu  sync.Mutex
	subs   map[int]chan string
	subSeq int
}

// New builds a Service over root ($LANA_HOME/tools), applies opts and runs one
// synchronous detection pass (REQ-TB-002).
func New(root string, opts ...Option) *Service {
	s := &Service{
		store:  NewStore(root),
		logger: slog.Default(),
		status: map[string]Status{},
		subs:   map[int]chan string{},
	}
	for _, opt := range opts {
		opt(s)
	}
	s.catalog = loadCatalog(s.logger)
	s.jobs = newJobManager(s.store, s.logger, func(job Job) { s.afterJob(job) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s.refreshAll(ctx)
	return s
}

// Store returns the file store; the runtime uses it for package paths.
func (s *Service) Store() *Store { return s.store }

// List returns every package, including broken ones (API-TB-001, REQ-TB-001).
func (s *Service) List(ctx context.Context) []Package {
	if err := ctx.Err(); err != nil {
		return nil
	}
	entries, err := s.entries()
	if err != nil {
		s.logger.Error("toolbox: list packages", "error", err)
		return nil
	}
	out := make([]Package, 0, len(entries))
	for _, item := range entries {
		out = append(out, s.packageOf(item))
	}
	return out
}

// Get returns one package, or ErrNotFound (API-TB-003, REQ-TB-001).
func (s *Service) Get(ctx context.Context, id string) (Package, error) {
	if err := ctx.Err(); err != nil {
		return Package{}, err
	}
	item, err := s.entry(id)
	if err != nil {
		return Package{}, err
	}
	return s.packageOf(item), nil
}

// entries returns the stored packages merged with the built-in catalog: a
// stored package wins over a catalog entry with the same id and is source
// "custom"; a catalog-only entry is source "catalog" (REQ-TB-001).
func (s *Service) entries() ([]Stored, error) {
	stored, err := s.store.List()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(stored))
	out := make([]Stored, 0, len(stored)+len(s.catalog.manifests))
	for _, item := range stored {
		seen[item.ID] = true
		// A directory without a tool.toml is a catalog install (bin/ and
		// logs/ only), not a broken custom package.
		if item.Problem != "" {
			if manifest, ok := s.catalog.byID[item.ID]; ok {
				out = append(out, Stored{ID: item.ID, Manifest: manifest, Source: SourceCatalog, UpdatedAt: item.UpdatedAt})
				continue
			}
		}
		item.Source = SourceCustom
		out = append(out, item)
	}
	for _, manifest := range s.catalog.manifests {
		if seen[manifest.ID] {
			continue
		}
		out = append(out, Stored{ID: manifest.ID, Manifest: manifest, Source: SourceCatalog})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// entry reads one package: the stored override first, then the catalog
// (REQ-TB-001).
func (s *Service) entry(id string) (Stored, error) {
	if !idPattern.MatchString(id) {
		return Stored{}, ErrNotFound
	}
	stored, err := s.store.entry(id)
	if err == nil {
		stored.Source = SourceCustom
		return stored, nil
	}
	if errors.Is(err, ErrNotFound) {
		if manifest, ok := s.catalog.byID[id]; ok {
			return Stored{ID: id, Manifest: manifest, Source: SourceCatalog}, nil
		}
	}
	return Stored{}, err
}

// getManifest returns the manifest of a stored or catalog package.
func (s *Service) getManifest(id string) (Manifest, error) {
	item, err := s.entry(id)
	if err != nil {
		return Manifest{}, err
	}
	if item.Problem != "" {
		return Manifest{}, &InvalidManifestError{Issues: []Issue{{Field: "tool.toml", Message: item.Problem}}}
	}
	return item.Manifest, nil
}

// inCatalog reports whether an id belongs to the built-in catalog (REQ-TB-001).
func (s *Service) inCatalog(id string) bool {
	_, ok := s.catalog.byID[id]
	return ok
}

// Add stores a new package from a manifest document. It returns the validation
// issues of an invalid manifest and ErrExists when the id is taken (REQ-TB-001).
func (s *Service) Add(ctx context.Context, manifestText string) (Package, []Issue, error) {
	if err := ctx.Err(); err != nil {
		return Package{}, nil, err
	}
	manifest, issues := ParseManifest(manifestText)
	if len(issues) > 0 {
		return Package{}, issues, nil
	}
	if s.store.exists(manifest.ID) || s.inCatalog(manifest.ID) {
		return Package{}, nil, ErrExists
	}
	if err := s.store.Save(manifest); err != nil {
		return Package{}, nil, err
	}
	s.setStatus(manifest.ID, s.store.Check(ctx, manifest))
	s.broadcast(manifest.ID)
	pkg, err := s.Get(ctx, manifest.ID)
	return pkg, nil, err
}

// Update replaces the manifest of an existing package. The id may not change.
// It returns ErrNotFound for an unknown package (API-TB-004, REQ-TB-001).
func (s *Service) Update(ctx context.Context, id, manifestText string) (Package, []Issue, error) {
	if err := ctx.Err(); err != nil {
		return Package{}, nil, err
	}
	if !s.store.exists(id) && !s.inCatalog(id) {
		return Package{}, nil, ErrNotFound
	}
	manifest, issues := ParseManifest(manifestText)
	if len(issues) > 0 {
		return Package{}, issues, nil
	}
	if manifest.ID != id {
		return Package{}, []Issue{{Field: "id", Message: fmt.Sprintf("id cannot change from %q to %q", id, manifest.ID)}}, nil
	}
	// Updating a catalog entry writes a stored override that wins from now on
	// (REQ-TB-001).
	if err := s.store.Save(manifest); err != nil {
		return Package{}, nil, err
	}
	s.setStatus(id, s.store.Check(ctx, manifest))
	s.broadcast(id)
	pkg, err := s.Get(ctx, id)
	return pkg, nil, err
}

// Delete removes a package home. It refuses an installed package unless
// uninstall is true (API-TB-005, REQ-TB-008).
func (s *Service) Delete(ctx context.Context, id string, uninstall bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stored, err := s.store.entry(id)
	if err != nil {
		// A catalog entry with no stored override cannot be removed: the
		// catalog stays (REQ-TB-001).
		if errors.Is(err, ErrNotFound) && s.inCatalog(id) {
			return ErrCatalog
		}
		return err
	}
	if s.jobs.isActive(id) {
		return ErrBusy
	}
	if stored.Problem == "" && s.currentState(id) == StateInstalled && !uninstall {
		return ErrInstalled
	}
	if err := s.store.Delete(id); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.status, id)
	s.mu.Unlock()
	// Removing a catalog override falls back to the built-in manifest, which
	// needs a fresh detection pass (REQ-TB-001).
	if manifest, ok := s.catalog.byID[id]; ok {
		s.setStatus(id, s.store.Check(ctx, manifest))
	}
	s.broadcast(id)
	return nil
}

// Install starts the install job of one method. A method below zero asks for
// the first method that fits this machine (API-TB-006, REQ-TB-003,
// REQ-TB-009).
func (s *Service) Install(ctx context.Context, id string, method int) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	manifest, err := s.getManifest(id)
	if err != nil {
		return Job{}, err
	}
	if method < 0 {
		auto, ok := manifest.AutoMethod()
		if !ok {
			return Job{}, fmt.Errorf("%w: no install method fits this machine", ErrMethodMismatch)
		}
		method = auto
	}
	if method >= len(manifest.Install) {
		return Job{}, fmt.Errorf("toolbox: install method %d does not exist", method)
	}
	if fits, reason := manifest.MethodFits(method); !fits {
		return Job{}, fmt.Errorf("%w: %s", ErrMethodMismatch, reason)
	}
	job, err := s.jobs.start(manifest, JobKindInstall, method)
	if err != nil {
		return Job{}, err
	}
	s.broadcast(id)
	return job, nil
}

// Uninstall starts the uninstall job of a package (API-TB-007, REQ-TB-008).
func (s *Service) Uninstall(ctx context.Context, id string) (Job, error) {
	if err := ctx.Err(); err != nil {
		return Job{}, err
	}
	manifest, err := s.getManifest(id)
	if err != nil {
		return Job{}, err
	}
	if s.currentState(id) == StateMissing {
		return Job{}, ErrNotInstalled
	}
	job, err := s.jobs.start(manifest, JobKindUninstall, -1)
	if err != nil {
		return Job{}, err
	}
	s.broadcast(id)
	return job, nil
}

// Check recomputes the status of one package (API-TB-008, REQ-TB-002).
func (s *Service) Check(ctx context.Context, id string) (Package, error) {
	if err := ctx.Err(); err != nil {
		return Package{}, err
	}
	manifest, err := s.getManifest(id)
	if err != nil {
		return Package{}, err
	}
	s.setStatus(id, s.store.Check(ctx, manifest))
	s.broadcast(id)
	return s.Get(ctx, id)
}

// Jobs returns a package's jobs, newest first (API-TB-009).
func (s *Service) Jobs(id string) []Job {
	return s.jobs.list(id)
}

// Log returns the tail of a job log and whether the job has finished
// (API-TB-009).
func (s *Service) Log(id, jobID string) (string, bool, error) {
	if !s.store.exists(id) && !s.inCatalog(id) {
		return "", true, ErrNotFound
	}
	return s.jobs.log(id, jobID)
}

// Run executes one command of a package (API-TB-009, REQ-TB-004, REQ-TB-007).
func (s *Service) Run(ctx context.Context, id, cmd string, input map[string]any, workspace string) (RunResult, error) {
	manifest, err := s.getManifest(id)
	if err != nil {
		return RunResult{}, err
	}
	return s.store.Run(ctx, manifest, cmd, input, workspace, s.secrets)
}

// Installed returns the packages whose state is installed (REQ-TB-004).
func (s *Service) Installed() []Package {
	entries, err := s.entries()
	if err != nil {
		return nil
	}
	out := make([]Package, 0, len(entries))
	for _, item := range entries {
		pkg := s.packageOf(item)
		if pkg.Status.State == StateInstalled {
			out = append(out, pkg)
		}
	}
	return out
}

// FindBinary returns the path of an installed package's binary whose detect
// binary matches, falling back to PATH (REQ-TB-006).
func (s *Service) FindBinary(binary string) (string, bool) {
	entries, err := s.entries()
	if err == nil {
		for _, item := range entries {
			if item.Problem != "" || item.Manifest.Detect.Binary != binary {
				continue
			}
			if s.currentState(item.ID) != StateInstalled {
				continue
			}
			if path, ok := s.store.Resolve(item.Manifest); ok {
				return path, true
			}
		}
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return "", false
	}
	return path, true
}

// Subscribe returns a stream of changed package ids and its unsubscribe
// function. Sends never block (REQ-TB-002).
func (s *Service) Subscribe() (<-chan string, func()) {
	s.subMu.Lock()
	id := s.subSeq
	s.subSeq++
	ch := make(chan string, subscriberBuffer)
	s.subs[id] = ch
	s.subMu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			s.subMu.Lock()
			if c, ok := s.subs[id]; ok {
				delete(s.subs, id)
				close(c)
			}
			s.subMu.Unlock()
		})
	}
	return ch, unsubscribe
}

// Close cancels the running jobs, waits for them and closes the change streams.
func (s *Service) Close() {
	s.jobs.close()
	s.subMu.Lock()
	for id, ch := range s.subs {
		delete(s.subs, id)
		close(ch)
	}
	s.subMu.Unlock()
}

// refreshAll runs one detection pass over every stored package (REQ-TB-002).
func (s *Service) refreshAll(ctx context.Context) {
	entries, err := s.entries()
	if err != nil {
		s.logger.Error("toolbox: list packages", "error", err)
		return
	}
	for _, item := range entries {
		if item.Problem != "" {
			s.setStatus(item.ID, Status{State: StateFailed, Error: item.Problem, CheckedAt: time.Now().UnixMilli()})
			continue
		}
		s.setStatus(item.ID, s.store.Check(ctx, item.Manifest))
	}
}

// afterJob runs after a job finishes: it records a failed install and rechecks
// every other outcome, then broadcasts the change (REQ-TB-002, REQ-TB-003).
func (s *Service) afterJob(job Job) {
	if job.Package == "" {
		return
	}
	if job.Kind == JobKindInstall && job.Status == JobFailed {
		s.setStatus(job.Package, Status{State: StateFailed, Error: job.Error, CheckedAt: time.Now().UnixMilli()})
		s.broadcast(job.Package)
		return
	}
	if manifest, err := s.getManifest(job.Package); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		s.setStatus(job.Package, s.store.Check(ctx, manifest))
		cancel()
	}
	s.broadcast(job.Package)
}

// packageOf assembles a Package from a stored entry.
func (s *Service) packageOf(item Stored) Package {
	source := item.Source
	if source == "" {
		source = SourceCustom
	}
	pkg := Package{Manifest: item.Manifest, Problem: item.Problem, UpdatedAt: item.UpdatedAt, Source: source}
	if item.Problem != "" {
		pkg.Manifest.ID = item.ID
		pkg.Status = Status{State: StateFailed, Error: item.Problem, CheckedAt: time.Now().UnixMilli()}
		return pkg
	}
	pkg.Status = s.statusOf(item.ID)
	pkg.Installing = s.jobs.isActive(item.ID)
	if pkg.Installing {
		pkg.Status.State = StateInstalling
	}
	return pkg
}

// statusOf returns the cached status of a package, or missing when unknown.
func (s *Service) statusOf(id string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if status, ok := s.status[id]; ok {
		return status
	}
	return Status{State: StateMissing}
}

// currentState returns the cached state of a package.
func (s *Service) currentState(id string) string {
	return s.statusOf(id).State
}

// setStatus records a detection result.
func (s *Service) setStatus(id string, status Status) {
	s.mu.Lock()
	s.status[id] = status
	s.mu.Unlock()
}

// broadcast publishes a changed package id; a full subscriber drops the event
// (REQ-TB-002).
func (s *Service) broadcast(id string) {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for _, ch := range s.subs {
		select {
		case ch <- id:
		default:
		}
	}
}

// PackageView is the package object of API-TB-001 (design "Web API").
type PackageView struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Homepage    string        `json:"homepage"`
	Status      string        `json:"status"`
	Version     string        `json:"version"`
	BinaryPath  string        `json:"binary_path"`
	Error       string        `json:"error"`
	Installing  bool          `json:"installing"`
	Commands    []CommandView `json:"commands"`
	Install     []InstallView `json:"install"`
	UpdatedAt   int64         `json:"updated_at"`
	// Revision 2 (REQ-TB-001/009/011).
	Source     string   `json:"source"`
	Icon       string   `json:"icon"`
	Category   string   `json:"category"`
	MCP        *MCPView `json:"mcp"`
	AutoMethod int      `json:"auto_method"`
}

// MCPView is the MCP object of a PackageView (REQ-TB-011).
type MCPView struct {
	Args []string `json:"args"`
}

// CommandView is one command row of a PackageView.
type CommandView struct {
	Name        string      `json:"name"`
	Tool        string      `json:"tool"`
	Description string      `json:"description"`
	Risk        string      `json:"risk"`
	Input       []InputView `json:"input"`
}

// InputView is one input field row of a command.
type InputView struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Required    bool     `json:"required"`
	Default     any      `json:"default"`
	Enum        []string `json:"enum"`
}

// InstallView is one install method row of a PackageView.
type InstallView struct {
	Label  string `json:"label"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	// Fits tells whether the method applies to this machine, and Reason why
	// not (REQ-TB-009).
	Fits   bool   `json:"fits"`
	Reason string `json:"reason"`
}

// View renders a Package as the PackageView of the web API.
func (p Package) View() PackageView {
	source := p.Source
	if source == "" {
		source = SourceCustom
	}
	view := PackageView{
		ID:          p.Manifest.ID,
		Name:        p.Manifest.Name,
		Description: p.Manifest.Description,
		Homepage:    p.Manifest.Homepage,
		Status:      p.Status.State,
		Version:     p.Status.Version,
		BinaryPath:  p.Status.BinaryPath,
		Error:       p.Status.Error,
		Installing:  p.Installing,
		UpdatedAt:   p.UpdatedAt,
		Source:      source,
		Icon:        p.Manifest.Icon,
		Category:    p.Manifest.Category,
		AutoMethod:  -1,
	}
	if view.Error == "" {
		view.Error = p.Problem
	}
	if method, ok := p.Manifest.AutoMethod(); ok {
		view.AutoMethod = method
	}
	if p.Manifest.MCP != nil {
		view.MCP = &MCPView{Args: p.Manifest.MCP.Args}
	}
	view.Commands = make([]CommandView, 0, len(p.Manifest.Commands))
	for _, c := range p.Manifest.Commands {
		view.Commands = append(view.Commands, commandView(p.Manifest.ID, c))
	}
	sort.Slice(view.Commands, func(i, j int) bool { return view.Commands[i].Name < view.Commands[j].Name })
	view.Install = make([]InstallView, 0, len(p.Manifest.Install))
	for i, md := range p.Manifest.Install {
		view.Install = append(view.Install, installView(p.Manifest, i, md))
	}
	return view
}

// commandView builds one command row, with its input fields sorted by name.
func commandView(pkg string, c Command) CommandView {
	view := CommandView{Name: c.Name, Tool: ToolName(pkg, c.Name), Description: c.Description, Risk: c.Risk}
	names := make([]string, 0, len(c.Input))
	for name := range c.Input {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		field := c.Input[name]
		view.Input = append(view.Input, InputView{
			Name:        name,
			Type:        typeOf(field),
			Description: field.Description,
			Required:    field.Required,
			Default:     field.Default,
			Enum:        field.Enum,
		})
	}
	return view
}

// installView builds one install method row, with whether the method fits this
// machine and why not (REQ-TB-009).
func installView(m Manifest, i int, md InstallMethod) InstallView {
	fits, reason := m.MethodFits(i)
	if strings.TrimSpace(md.Command) != "" {
		return InstallView{Label: md.Label, Kind: "command", Detail: md.Command, Fits: fits, Reason: reason}
	}
	return InstallView{Label: md.Label, Kind: "download", Detail: md.URL, Fits: fits, Reason: reason}
}

// MCPServer describes one MCP server a package provides (REQ-TB-011).
type MCPServer struct {
	Package string            `json:"package"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

// MCPServers returns one MCP server per installed package that declares an
// [mcp] spec: the resolved binary, the args with {{data}} and {{bin}} rendered,
// and the resolved env. A package whose binary is missing, or whose secret env
// cannot be resolved, is skipped (REQ-TB-007, REQ-TB-011).
func (s *Service) MCPServers() []MCPServer {
	entries, err := s.entries()
	if err != nil {
		s.logger.Error("toolbox: list packages for mcp", "error", err)
		return nil
	}
	out := make([]MCPServer, 0, len(entries))
	for _, item := range entries {
		if item.Problem != "" || item.Manifest.MCP == nil {
			continue
		}
		if s.currentState(item.ID) != StateInstalled {
			continue
		}
		path, ok := s.store.Resolve(item.Manifest)
		if !ok {
			continue
		}
		env, ok := s.mcpEnv(item.ID, item.Manifest.MCP)
		if !ok {
			continue
		}
		// The profile the server keeps needs its directory to exist
		// (REQ-TB-011).
		if err := os.MkdirAll(filepath.Join(s.store.DataDir(item.ID), "profile"), 0o700); err != nil {
			s.logger.Error("toolbox: create mcp data dir", "package", item.ID, "error", err)
			continue
		}
		out = append(out, MCPServer{
			Package: item.ID,
			Command: path,
			Args:    renderMCPArgs(item.Manifest, s.store),
			Env:     env,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Package < out[j].Package })
	return out
}

// renderMCPArgs renders the {{data}} and {{bin}} templates of an [mcp] spec
// (REQ-TB-011).
func renderMCPArgs(m Manifest, store *Store) []string {
	replacer := strings.NewReplacer("{{data}}", store.DataDir(m.ID), "{{bin}}", store.BinDir(m.ID))
	args := make([]string, 0, len(m.MCP.Args))
	for _, arg := range m.MCP.Args {
		args = append(args, replacer.Replace(arg))
	}
	return args
}

// mcpEnv resolves the [mcp.env] values of a package like command env: a
// "secret:<NAME>" value goes through the resolver. A missing secret makes the
// server unusable, so it is skipped (REQ-TB-007, REQ-TB-011).
func (s *Service) mcpEnv(pkg string, spec *MCPSpec) (map[string]string, bool) {
	if len(spec.Env) == 0 {
		return nil, true
	}
	env := make(map[string]string, len(spec.Env))
	for name, value := range spec.Env {
		key, isSecret := strings.CutPrefix(value, "secret:")
		if !isSecret {
			env[name] = value
			continue
		}
		if s.secrets == nil {
			s.logger.Warn("toolbox: mcp secret is missing", "package", pkg, "secret", key)
			return nil, false
		}
		resolved, ok := s.secrets(key)
		if !ok {
			s.logger.Warn("toolbox: mcp secret is missing", "package", pkg, "secret", key)
			return nil, false
		}
		env[name] = resolved
	}
	return env, true
}
