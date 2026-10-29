package telegram

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ridhomujizat/lana-harness/core"
	"github.com/ridhomujizat/lana-harness/internal/agenthome"
)

// TurnResult is the outcome of one turn.
type TurnResult struct {
	Text         string
	StopReason   string
	InputTokens  int64
	OutputTokens int64
}

// Backend is the runtime port used by the channel. Chat IDs are passed as session addresses.
type Backend interface {
	ActiveAgent(ctx context.Context, address string) (string, error)
	ChatSession(ctx context.Context, address, agent string) (core.SessionInfo, error)
	NewSession(ctx context.Context, address, agent string) (core.SessionInfo, error)
	// Lease takes the session lease; holding it already is not an error.
	Lease(ctx context.Context, sessionID string) error
	SessionInfo(ctx context.Context, sessionID string) (core.SessionInfo, error)
	RunTurn(ctx context.Context, sessionID, prompt string) (TurnResult, error)
	Send(ctx context.Context, sessionID, text string) error
	Cancel(ctx context.Context, sessionID string) error
	SubscribeLive(ctx context.Context, sessionID string) (*core.Subscription[core.LiveFrame], error)
	Compact(ctx context.Context, sessionID string) (before, after int64, err error)
	Memory(agent string) (user, memory []string, err error)
	MCPStatus(agent string) []core.MCPServerStatus
	Skills(agent string) []agenthome.Skill
	Policy(ctx context.Context, sessionID string) (string, error)
	AgentNames() []string
	AgentModel(agent string) string
}

// Commands is the list registered with setMyCommands (API-TG-005).
var Commands = []Command{
	{"new", "Start a new session"},
	{"agent", "Show or switch the agent"},
	{"memory", "Show this agent's memory"},
	{"mcp", "Show this agent's MCP servers"},
	{"skills", "Show this agent's skills"},
	{"compact", "Summarize the session history"},
	{"cancel", "Stop the running turn"},
	{"status", "Agent, model, and session"},
	{"help", "List commands"},
}

// Channel runs the Telegram bot.
type Channel struct {
	api     *Client
	cfg     Config
	backend Backend

	// Timing knobs; tests shorten them.
	EditInterval    time.Duration
	TypingInterval  time.Duration
	ApprovalTimeout time.Duration
	PollTimeout     int
	MaxBackoff      time.Duration

	bot User

	mu        sync.Mutex
	chats     map[int64]*chat
	sessions  map[string]*chat
	approvals map[string]*approval
	wg        sync.WaitGroup
	fatal     chan error
}

// New creates a channel. token is the bot token value.
func New(cfg Config, token string, backend Backend) *Channel {
	return &Channel{
		api: NewClient(cfg.APIBase, token), cfg: cfg, backend: backend,
		EditInterval: time.Second, TypingInterval: 5 * time.Second, ApprovalTimeout: 10 * time.Minute,
		PollTimeout: 30, MaxBackoff: 30 * time.Second,
		chats: map[int64]*chat{}, sessions: map[string]*chat{}, approvals: map[string]*approval{},
		fatal: make(chan error, 1),
	}
}

// Start checks the token and registers commands. It returns the bot user.
func (c *Channel) Start(ctx context.Context) (User, error) {
	bot, err := c.api.GetMe(ctx)
	if err != nil {
		return User{}, err
	}
	c.bot = bot
	if err := c.api.SetMyCommands(ctx, Commands); err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return User{}, err
		}
		slog.Warn("telegram: setMyCommands failed", "error", err)
	}
	return bot, nil
}

// Run polls until ctx ends or the token is rejected. It waits for chat workers before returning.
func (c *Channel) Run(ctx context.Context) error {
	defer c.wg.Wait()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var offset int64
	backoff := min(time.Second, c.MaxBackoff)
	for {
		select {
		case err := <-c.fatal:
			return err
		default:
		}
		updates, err := c.api.GetUpdates(ctx, offset, c.PollTimeout)
		if ctx.Err() != nil {
			select {
			case err := <-c.fatal:
				return err
			default:
			}
			return nil
		}
		if errors.Is(err, ErrUnauthorized) {
			return err
		}
		if err != nil {
			slog.Warn("telegram: getUpdates failed", "error", err, "retry_in", backoff)
			if sleepCtx(ctx, backoff) != nil {
				return nil
			}
			backoff *= 2
			if backoff > c.MaxBackoff {
				backoff = c.MaxBackoff
			}
			continue
		}
		backoff = min(time.Second, c.MaxBackoff)
		for _, update := range updates {
			c.dispatch(ctx, update)
			offset = update.UpdateID + 1
		}
	}
}

// failFatal stops Run with err (used for 401 outside the poll loop).
func (c *Channel) failFatal(err error) {
	select {
	case c.fatal <- err:
	default:
	}
}

func (c *Channel) dispatch(ctx context.Context, update Update) {
	var chatID int64
	var from *User
	switch {
	case update.Message != nil:
		chatID, from = update.Message.Chat.ID, update.Message.From
	case update.CallbackQuery != nil && update.CallbackQuery.Message != nil:
		chatID, from = update.CallbackQuery.Message.Chat.ID, &update.CallbackQuery.From
	default:
		return
	}
	agent, ok := c.cfg.Routes[chatID]
	if !ok {
		args := []any{"chat_id", chatID}
		if from != nil {
			args = append(args, "user_id", from.ID, "username", from.Username)
		}
		slog.Info("telegram: ignored update from chat without route", args...)
		return
	}
	w := c.worker(ctx, chatID, agent)
	select {
	case w.updates <- update:
	default:
		slog.Warn("telegram: chat queue full; update dropped", "chat_id", chatID)
	}
}

func (c *Channel) worker(ctx context.Context, chatID int64, routeAgent string) *chat {
	c.mu.Lock()
	defer c.mu.Unlock()
	if w, ok := c.chats[chatID]; ok {
		return w
	}
	w := &chat{ch: c, id: chatID, routeAgent: routeAgent, updates: make(chan Update, 64)}
	c.chats[chatID] = w
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		w.run(ctx)
	}()
	return w
}

func (c *Channel) bindSession(sessionID string, w *chat) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions[sessionID] = w
}

func (c *Channel) chatForSession(sessionID string) *chat {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessions[sessionID]
}

// isOwner applies the owner rule of API-TG-001.
func (c *Channel) isOwner(chatType string, userID int64) bool {
	return chatType == "private" || c.cfg.OwnerIDs[userID]
}

// apiErr handles a Bot API error from a send or edit: 401 stops the daemon, the rest is logged.
func (c *Channel) apiErr(call string, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, ErrUnauthorized) {
		c.failFatal(err)
		return
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Code == 400 && containsFold(apiErr.Description, "message is not modified") {
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	slog.Warn("telegram: "+call+" failed", "error", err)
}
