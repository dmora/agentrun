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

	// OptionMode sets the operating mode for the session.
	// Backends that support modes map this to their native mechanism.
	// Backends that don't recognize this option silently ignore it.
	// Values should be Mode constants (ModePlan, ModeAct).
	OptionMode = "mode"

	// OptionHITL controls human-in-the-loop supervision.
	// When "on" (or absent), the backend requires human approval for
	// actions with side effects. When "off", autonomous operation.
	// Values should be HITL constants (HITLOn, HITLOff).
	OptionHITL = "hitl"

	// OptionResumeID sets the backend-assigned session identifier for resume.
	// Consumers capture this value from MessageInit.Content after the first
	// session, persist it, and set it here for subsequent sessions.
	// When set, backends include their native resume flag
	// (e.g., --resume for Claude, --session for OpenCode).
	// Value format is backend-specific and opaque to the root package.
	// Values are not portable across backends.
	//
	// An empty MessageInit.Content signals that the backend could not
	// capture a session ID (e.g., invalid format from the agent runtime).
	// Consumers should treat empty Content as "no ID available" and avoid
	// persisting it for future resume.
	OptionResumeID = "resume_id"
)

// Mode represents the operating mode for a session.
type Mode string

const (
	// ModePlan requests analysis-only behavior.
	ModePlan Mode = "plan"

	// ModeAct authorizes the agent to take actions.
	ModeAct Mode = "act"
)

// Valid reports whether m is a recognized Mode value.
func (m Mode) Valid() bool {
	return m == ModePlan || m == ModeAct
}

// HITL represents the human-in-the-loop setting for a session.
type HITL string

const (
	// HITLOn requires human approval for actions with side effects.
	HITLOn HITL = "on"

	// HITLOff enables autonomous operation without human prompts.
	HITLOff HITL = "off"
)

// Valid reports whether h is a recognized HITL value.
func (h HITL) Valid() bool {
	return h == HITLOn || h == HITLOff
}

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
