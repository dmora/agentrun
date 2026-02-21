package agentrun

// Session is the minimal session state passed to engines.
//
// Session is a value type — it carries identity and configuration but
// no runtime state (no mutexes, no channels, no process handles).
// Orchestrators that need richer state should embed or wrap Session.
type Session struct {
	// ID uniquely identifies the session.
	ID string `json:"id"`

	// AgentID identifies which agent specification to use.
	AgentID string `json:"agent_id,omitempty"`

	// CWD is the working directory for the agent process.
	CWD string `json:"cwd"`

	// Model specifies the AI model to use (e.g., "claude-sonnet-4-5-20250514").
	Model string `json:"model,omitempty"`

	// Prompt is the initial prompt or message for the session.
	Prompt string `json:"prompt,omitempty"`

	// Options holds backend-specific key-value configuration.
	// CLI backends use this for flags like permission mode.
	// API backends use this for endpoint configuration.
	Options map[string]string `json:"options,omitempty"`
}
