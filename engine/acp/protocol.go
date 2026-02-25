package acp

import "encoding/json"

// JSON-RPC 2.0 method constants for the Agent Client Protocol.
const (
	MethodInitialize       = "initialize"
	MethodSessionNew       = "session/new"
	MethodSessionLoad      = "session/load"
	MethodSessionPrompt    = "session/prompt"
	MethodSessionUpdate    = "session/update"
	MethodSessionCancel    = "session/cancel"
	MethodSessionSetMode   = "session/set_mode"
	MethodSessionSetConfig = "session/set_config_option"
	MethodRequestPerm      = "session/request_permission"
	MethodShutdown         = "shutdown"
)

// ACP protocol and client identity constants.
const (
	protocolVersion = 1 // ACP spec v0.10.8 — integer, not semver
	clientName      = "agentrun"
	clientVersion   = "0.1.0"
)

// --- Initialize ---

// initializeParams is sent to the agent to begin the capability handshake.
type initializeParams struct {
	ProtocolVersion    int                 `json:"protocolVersion"`
	ClientCapabilities *clientCapabilities `json:"clientCapabilities,omitempty"`
	ClientInfo         *implementation     `json:"clientInfo,omitempty"`
}

// initializeResult is the agent's response to initialize.
type initializeResult struct {
	ProtocolVersion   int                `json:"protocolVersion"`
	AgentCapabilities *agentCapabilities `json:"agentCapabilities,omitempty"`
	AgentInfo         *implementation    `json:"agentInfo,omitempty"`
	AuthMethods       []authMethod       `json:"authMethods,omitempty"`
}

// implementation identifies a client or agent (used for both clientInfo and agentInfo).
type implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// clientCapabilities declares which client-side operations the client supports.
type clientCapabilities struct {
	FS       *fileSystemCapability `json:"fs,omitempty"`
	Terminal bool                  `json:"terminal,omitempty"`
}

// fileSystemCapability declares file system operations the client supports.
type fileSystemCapability struct {
	ReadTextFile  bool `json:"readTextFile,omitempty"`
	WriteTextFile bool `json:"writeTextFile,omitempty"`
}

// agentCapabilities declares what the agent supports.
type agentCapabilities struct {
	LoadSession bool `json:"loadSession,omitempty"`
}

// authMethod describes an authentication method offered by the agent.
type authMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// --- Session ---

// newSessionParams creates a new agent session.
type newSessionParams struct {
	CWD        string      `json:"cwd"`
	MCPServers []mcpServer `json:"mcpServers"`
}

// newSessionResult is the response to session/new.
type newSessionResult struct {
	SessionID     string                `json:"sessionId"`
	Modes         *sessionModeState     `json:"modes,omitempty"`
	Models        *sessionModelState    `json:"models,omitempty"`
	ConfigOptions []sessionConfigOption `json:"configOptions,omitempty"`
}

// loadSessionParams resumes an existing session.
type loadSessionParams struct {
	SessionID  string      `json:"sessionId"`
	CWD        string      `json:"cwd"`
	MCPServers []mcpServer `json:"mcpServers"`
}

// loadSessionResult is the response to session/load.
// NOTE: no SessionID field — the caller uses the resumeID directly.
type loadSessionResult struct {
	Modes         *sessionModeState     `json:"modes,omitempty"`
	Models        *sessionModelState    `json:"models,omitempty"`
	ConfigOptions []sessionConfigOption `json:"configOptions,omitempty"`
}

// mcpServer describes an MCP server to attach to the session (stdio-only for MVP).
type mcpServer struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// sessionModeState describes the agent's current and available operating modes.
type sessionModeState struct {
	CurrentModeID  string        `json:"currentModeId"`
	AvailableModes []sessionMode `json:"availableModes"`
}

// sessionMode describes a single operating mode.
type sessionMode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// sessionModelState describes the agent's current and available models.
type sessionModelState struct {
	CurrentModelID  string      `json:"currentModelId"`
	AvailableModels []modelInfo `json:"availableModels"`
}

// modelInfo describes a model available to the agent.
type modelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// sessionConfigOption describes a configurable session option.
type sessionConfigOption struct {
	ID           string               `json:"id"`
	Name         string               `json:"name"`
	Category     string               `json:"category,omitempty"`
	Type         string               `json:"type,omitempty"`
	CurrentValue string               `json:"currentValue,omitempty"`
	Options      []configOptionChoice `json:"options,omitempty"`
}

// configOptionChoice is one selectable value for a config option.
type configOptionChoice struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

// --- Prompt ---

// contentBlock is a single content element in a prompt (MVP: text-only).
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// promptParams sends a user message to the session.
type promptParams struct {
	SessionID string         `json:"sessionId"`
	Prompt    []contentBlock `json:"prompt"`
}

// promptResult is the response when a prompt turn completes.
type promptResult struct {
	StopReason string    `json:"stopReason,omitempty"`
	Usage      *acpUsage `json:"usage,omitempty"`
}

// acpUsage contains token usage from a prompt turn.
type acpUsage struct {
	InputTokens       int `json:"inputTokens"`
	OutputTokens      int `json:"outputTokens"`
	TotalTokens       int `json:"totalTokens"`
	ThoughtTokens     int `json:"thoughtTokens,omitempty"`
	CachedReadTokens  int `json:"cachedReadTokens,omitempty"`
	CachedWriteTokens int `json:"cachedWriteTokens,omitempty"`
}

// --- Updates (notifications from agent) ---

// sessionNotification is the outer envelope for session/update notifications.
type sessionNotification struct {
	SessionID string          `json:"sessionId"`
	Update    json.RawMessage `json:"update"`
}

// sessionUpdateHeader extracts the discriminator from the inner update object.
type sessionUpdateHeader struct {
	SessionUpdate string `json:"sessionUpdate"`
}

// --- Permission (internal wire types — NOT exposed in PermissionHandler) ---

// requestPermissionParams is the ACP wire format for permission requests.
type requestPermissionParams struct {
	SessionID string          `json:"sessionId"`
	ToolCall  toolCallUpdate  `json:"toolCall"`
	Options   []permissionOpt `json:"options"`
}

// toolCallUpdate describes a tool call in permission and update contexts.
type toolCallUpdate struct {
	ToolCallID string          `json:"toolCallId"`
	Title      string          `json:"title,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	Status     string          `json:"status,omitempty"`
	Content    json.RawMessage `json:"content,omitempty"`
	RawInput   json.RawMessage `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage `json:"rawOutput,omitempty"`
}

// permissionOpt is a single option in a permission request.
type permissionOpt struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
}

// requestPermissionResult is the response to a permission request.
type requestPermissionResult struct {
	Outcome requestPermissionOutcome `json:"outcome"`
}

// requestPermissionOutcome is the selected outcome.
type requestPermissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}

// --- Config Setting ---

// setModeParams sets the session operating mode.
type setModeParams struct {
	SessionID string `json:"sessionId"`
	ModeID    string `json:"modeId"`
}

// setConfigOptionParams sets a session config option.
type setConfigOptionParams struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}
