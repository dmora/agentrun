package agentrun

import "maps"

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
	// Consumers capture this value from MessageInit.ResumeID after the first
	// session, persist it, and set it here for subsequent sessions.
	// When set, backends include their native resume flag
	// (e.g., --resume for Claude, --session for OpenCode).
	// Value format is backend-specific and opaque to the root package.
	// Values are not portable across backends.
	//
	// An empty MessageInit.ResumeID signals that the backend could not
	// capture a session ID (e.g., invalid format from the agent runtime).
	// Consumers should treat empty ResumeID as "no ID available" and avoid
	// persisting it for future resume.
	OptionResumeID = "resume_id"

	// OptionAgentID identifies which agent specification to use.
	// Cross-cutting: OpenCode maps to --agent <id>, ADK uses agent registry.
	// Backends that don't support agent selection silently ignore this.
	OptionAgentID = "agent_id"

	// OptionEffort controls reasoning depth/quality tradeoff.
	// Value should be an Effort constant (low, medium, high, max).
	// Backend support varies — unsupported values are silently skipped:
	//   - Claude CLI: low, medium, high (maps to --effort)
	//   - Codex CLI: low, medium, high, max (maps to -c model_reasoning_effort; max → "xhigh")
	//   - OpenCode: low, high, max (maps to --variant; medium has no equivalent)
	//
	// When set and mappable to the backend's native values, OptionEffort
	// takes precedence over backend-specific effort options (e.g.,
	// opencode.OptionVariant). When set but unmappable (e.g., "medium"
	// on OpenCode), the backend falls through to its own option.
	// When absent, backend-specific options are used.
	OptionEffort = "effort"

	// OptionAddDirs specifies additional directories the agent may access
	// beyond CWD. Value is newline-separated absolute paths.
	//
	// Backend support: Claude (--add-dir), Codex (--add-dir).
	// Backends without directory scoping silently ignore this option.
	OptionAddDirs = "add_dirs"
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

// Effort controls reasoning depth/quality tradeoff.
type Effort string

const (
	// EffortLow requests minimal reasoning for speed.
	EffortLow Effort = "low"

	// EffortMedium requests balanced reasoning (default for most models).
	EffortMedium Effort = "medium"

	// EffortHigh requests thorough reasoning for complex tasks.
	EffortHigh Effort = "high"

	// EffortMax requests maximum reasoning depth.
	EffortMax Effort = "max"
)

// Valid reports whether e is a recognized Effort value.
func (e Effort) Valid() bool {
	return e == EffortLow || e == EffortMedium || e == EffortHigh || e == EffortMax
}

// Session is the minimal session state passed to engines.
//
// Session is a value type — it carries identity and configuration but
// no runtime state (no mutexes, no channels, no process handles).
// Orchestrators that need richer state should embed or wrap Session.
type Session struct {
	// ID uniquely identifies the session.
	ID string `json:"id"`

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

	// Env holds additional environment variables for the agent process.
	// These are merged with the parent process environment via MergeEnv —
	// os.Environ() provides the base, and Env entries override matching keys.
	// Nil or empty means inherit parent environment unchanged.
	//
	// Keys must not be empty, contain '=' or null bytes.
	// Values must not contain null bytes.
	//
	// Security note: consumers are responsible for what they pass.
	// The library validates key syntax but does not blocklist specific
	// variable names (e.g., LD_PRELOAD, PATH). Treat Session.Env like
	// cmd.Env — the caller owns the security boundary.
	Env map[string]string `json:"env,omitempty"`
}

// Clone returns a deep copy of s, cloning the Options and Env maps.
// Use Clone before mutating a session that may be shared.
func (s Session) Clone() Session {
	s.Options = maps.Clone(s.Options)
	s.Env = maps.Clone(s.Env)
	return s
}

// MergeEnv returns base with extra entries appended as "key=value" pairs.
//
// Override semantics: extra entries are appended after base. When a key
// exists in both base and extra, exec.Cmd uses last-wins semantics on
// most platforms — the appended value takes effect. The original entry
// remains in the slice but is shadowed.
//
// Nil contract: returns nil when extra is empty. Callers should pass nil
// to exec.Cmd.Env to inherit the parent environment unchanged. When extra
// is non-empty, os.Environ() is snapshotted at call time.
//
// Engines call: MergeEnv(os.Environ(), session.Env)
func MergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return nil
	}
	result := make([]string, 0, len(base)+len(extra))
	result = append(result, base...)
	for k, v := range extra {
		result = append(result, k+"="+v)
	}
	return result
}
