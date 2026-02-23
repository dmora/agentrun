package agentrun

// Well-known option keys for Session.Options.
//
// These keys define cross-cutting concepts that multiple backends may
// support. Backends silently ignore keys they don't recognize.
// Backend-specific options are defined in their own packages.
const (
	// OptionSystemPrompt sets the system prompt for the session.
	// Value is the raw prompt string.
	OptionSystemPrompt = "system_prompt"

	// OptionMaxTurns limits the number of agentic turns per invocation.
	// Value is a positive integer string (e.g., "5").
	OptionMaxTurns = "max_turns"

	// OptionThinkingBudget controls the model's thinking/reasoning output.
	// When set to a positive integer string (e.g., "10000"), backends that
	// support extended thinking will emit MessageThinking and
	// MessageThinkingDelta messages with the model's reasoning content.
	//
	// The value interpretation is backend-specific:
	//   - Claude CLI: token count, maps to --max-thinking-tokens
	//   - Other backends: may accept token counts, levels, or other formats
	//
	// When empty or absent, thinking output is disabled (backend default).
	OptionThinkingBudget = "thinking_budget"
)

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

	// Options holds session configuration as key-value pairs.
	// Use well-known Option* constants for cross-cutting features
	// (e.g., OptionSystemPrompt, OptionMaxTurns). Backend-specific
	// options are defined in their respective packages.
	Options map[string]string `json:"options,omitempty"`
}
