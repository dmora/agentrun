# Agent Client Protocol (ACP) -- Complete Reference for Go Client Implementation

> Research date: 2026-02-24
> Protocol version: 1 (integer, not semver)
> Spec version: v0.10.8 (2026-02-04)
> TypeScript SDK: v0.14.1 | Go community SDK: github.com/ironpark/acp-go
> Official repo: github.com/zed-industries/agent-client-protocol (Apache-2.0)

---

## Table of Contents

1. [Protocol Overview](#1-protocol-overview)
2. [Transport Layer](#2-transport-layer)
3. [Initialization](#3-initialization)
4. [Authentication](#4-authentication)
5. [Session Setup](#5-session-setup)
6. [Prompt Turn Lifecycle](#6-prompt-turn-lifecycle)
7. [Session Updates -- Complete Catalog](#7-session-updates--complete-catalog)
8. [Tool Calls](#8-tool-calls)
9. [Permission System](#9-permission-system)
10. [Session Modes](#10-session-modes)
11. [Session Config Options](#11-session-config-options)
12. [Slash Commands](#12-slash-commands)
13. [Agent Plan](#13-agent-plan)
14. [File System Operations](#14-file-system-operations)
15. [Terminal Operations](#15-terminal-operations)
16. [Content Blocks](#16-content-blocks)
17. [Error Handling](#17-error-handling)
18. [Extensibility](#18-extensibility)
19. [Complete Type Reference](#19-complete-type-reference)
20. [Real-World Implementations](#20-real-world-implementations)
21. [Delta from Current agentrun/engine/acp](#21-delta-from-current-agentrunengineacp)
22. [Ambiguities and Spec Gaps](#22-ambiguities-and-spec-gaps)

---

## 1. Protocol Overview

ACP enables bidirectional communication between **Agents** (AI-powered coding programs) and **Clients** (editors/IDEs) using **JSON-RPC 2.0** over stdio.

### Communication Model

- **Methods**: Request-response exchanges (have `id` field, expect result or error)
- **Notifications**: Fire-and-forget (no `id` field, no response expected)

### Protocol Phases

```
1. Initialization:   initialize -> authenticate (if needed)
2. Session Setup:    session/new  OR  session/load
3. Prompt Turns:     session/prompt -> session/update* -> response
                     (repeatable, with session/cancel available)
```

### Method Registry

**Agent Methods** (Client -> Agent):

| Method                      | Type         | Required |
|-----------------------------|--------------|----------|
| `initialize`                | Request      | Yes      |
| `authenticate`              | Request      | No*      |
| `session/new`               | Request      | Yes      |
| `session/load`              | Request      | No**     |
| `session/prompt`            | Request      | Yes      |
| `session/cancel`            | Notification | Yes      |
| `session/set_mode`          | Request      | No       |
| `session/set_config_option` | Request      | No       |

\* Required only when `authMethods` is non-empty in initialize response.
\** Only when agent advertises `loadSession` capability.

**Client Methods** (Agent -> Client):

| Method                     | Type         | Required |
|----------------------------|--------------|----------|
| `session/update`           | Notification | Yes      |
| `session/request_permission` | Request    | Yes      |
| `fs/read_text_file`        | Request      | No***    |
| `fs/write_text_file`       | Request      | No***    |
| `terminal/create`          | Request      | No****   |
| `terminal/output`          | Request      | No****   |
| `terminal/wait_for_exit`   | Request      | No****   |
| `terminal/kill`            | Request      | No****   |
| `terminal/release`         | Request      | No****   |

\*** Only when client advertises `fs.readTextFile` / `fs.writeTextFile`.
\**** Only when client advertises `terminal` capability.

---

## 2. Transport Layer

### stdio Transport (Required)

- Client spawns agent as subprocess
- Agent reads JSON-RPC messages from **stdin**, writes to **stdout**
- Messages are delimited by newline (`\n`)
- Messages **MUST NOT** contain embedded newlines
- Agent **MUST NOT** write non-ACP content to stdout
- Client **MUST NOT** write non-ACP content to agent's stdin
- Agent MAY write UTF-8 to stderr for logging
- All messages are UTF-8 encoded

### Streamable HTTP Transport (Draft)

Listed as draft proposal, not yet specified. Not relevant for implementation.

### Message Format

Every message is a single JSON-RPC 2.0 object on one line:

```json
{"jsonrpc":"2.0","id":0,"method":"initialize","params":{...}}
```

---

## 3. Initialization

### Method: `initialize`

**Client Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 0,
  "method": "initialize",
  "params": {
    "protocolVersion": 1,
    "clientCapabilities": {
      "fs": {
        "readTextFile": true,
        "writeTextFile": true
      },
      "terminal": true
    },
    "clientInfo": {
      "name": "agentrun",
      "title": "agentrun Go Library",
      "version": "0.1.0"
    }
  }
}
```

**Agent Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 0,
  "result": {
    "protocolVersion": 1,
    "agentCapabilities": {
      "loadSession": true,
      "promptCapabilities": {
        "image": true,
        "audio": true,
        "embeddedContext": true
      },
      "mcpCapabilities": {
        "http": true,
        "sse": true
      },
      "sessionCapabilities": {
        "fork": {},
        "list": {},
        "resume": {}
      }
    },
    "agentInfo": {
      "name": "opencode",
      "title": "OpenCode",
      "version": "1.0.0"
    },
    "authMethods": []
  }
}
```

### Type Definitions

```
InitializeRequest {
  protocolVersion:    ProtocolVersion (integer, required) -- currently 1
  clientCapabilities: ClientCapabilities (optional)
  clientInfo:         Implementation (optional)
  _meta:              map[string]any (optional)
}

InitializeResponse {
  protocolVersion:    ProtocolVersion (integer, required)
  agentCapabilities:  AgentCapabilities (optional)
  agentInfo:          Implementation (optional)
  authMethods:        []AuthMethod (optional)
  _meta:              map[string]any (optional)
}

Implementation {
  name:    string (required) -- programmatic identifier
  title:   string (optional) -- human-readable display name
  version: string (required) -- semver version
  _meta:   map[string]any (optional)
}

ClientCapabilities {
  fs:       FileSystemCapability (optional)
  terminal: bool (optional)
  _meta:    map[string]any (optional)
}

FileSystemCapability {
  readTextFile:  bool (optional)
  writeTextFile: bool (optional)
  _meta:         map[string]any (optional)
}

AgentCapabilities {
  loadSession:         bool (optional, default false)
  promptCapabilities:  PromptCapabilities (optional)
  mcpCapabilities:     McpCapabilities (optional)
  sessionCapabilities: SessionCapabilities (optional)
  _meta:               map[string]any (optional)
}

PromptCapabilities {
  image:           bool (optional, default false)
  audio:           bool (optional, default false)
  embeddedContext: bool (optional, default false)
  _meta:           map[string]any (optional)
}

McpCapabilities {
  http: bool (optional, default false)
  sse:  bool (optional, default false, deprecated)
  _meta: map[string]any (optional)
}

SessionCapabilities {
  fork:   SessionForkCapabilities (optional)
  list:   SessionListCapabilities (optional)
  resume: SessionResumeCapabilities (optional)
  _meta:  map[string]any (optional)
}
```

### Version Negotiation Rules

1. Client sends its **latest supported** protocol version (currently `1`)
2. Agent responds with the **same or earlier** version it supports
3. Client MUST close connection if it cannot support the agent's version
4. Protocol version is a **single integer** (not semver) identifying MAJOR version only
5. New capabilities are NOT considered breaking changes
6. All omitted capabilities MUST be treated as UNSUPPORTED

---

## 4. Authentication

### Method: `authenticate`

Called after `initialize` if the response includes non-empty `authMethods`.

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "authenticate",
  "params": {
    "methodId": "api_key"
  }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {}
}
```

### AuthMethod Type

```
AuthMethod {
  id:          string (required) -- unique identifier
  name:        string (required) -- human-readable name
  description: string (optional)
  _meta:       map[string]any (optional)
}
```

### Authentication Method Types (RFD -- proposed, not yet stable)

Three auth types are proposed in the RFD process:

1. **Agent Authentication** (`type: "agent"`): Agent manages its own auth flow
2. **Environment Variable** (`type: "env_var"`): Client passes credentials as env vars
   - Additional fields: `varName`, `link`
3. **Terminal Authentication** (`type: "terminal"`): Agent runs interactive terminal login
   - Additional fields: `args`, `env`

### Auth Error

When authentication fails, the agent returns error code `-32000` (AUTH_REQUIRED):
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "error": {
    "code": -32000,
    "message": "Authentication required",
    "authMethods": [{"id": "chatgpt", "name": "Login with ChatGPT"}]
  }
}
```

**AMBIGUITY FLAG**: The `authMethods` field on the error object is from an RFD, not the stable schema. The stable ErrorCode type does not define additional fields on errors beyond `code`, `message`, `data`.

---

## 5. Session Setup

### Method: `session/new`

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "session/new",
  "params": {
    "cwd": "/home/user/project",
    "mcpServers": []
  }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "sessionId": "sess_abc123def456",
    "modes": {
      "currentModeId": "ask",
      "availableModes": [
        {"id": "ask", "name": "Ask", "description": "Request permission..."},
        {"id": "code", "name": "Code", "description": "Write and modify..."}
      ]
    },
    "configOptions": [
      {
        "id": "model",
        "name": "Model",
        "category": "model",
        "type": "select",
        "currentValue": "claude-sonnet-4-20250514",
        "options": [
          {"value": "claude-sonnet-4-20250514", "name": "Claude Sonnet 4"}
        ]
      }
    ],
    "models": {
      "currentModelId": "claude-sonnet-4-20250514",
      "availableModels": [
        {"id": "claude-sonnet-4-20250514", "name": "Claude Sonnet 4"}
      ]
    }
  }
}
```

### Type Definitions

```
NewSessionRequest {
  cwd:        string (required) -- absolute path
  mcpServers: []McpServer (required, may be empty array)
  _meta:      map[string]any (optional)
}

NewSessionResponse {
  sessionId:     SessionId (string, required)
  modes:         SessionModeState (optional)
  configOptions: []SessionConfigOption (optional)
  models:        SessionModelState (optional)
  _meta:         map[string]any (optional)
}

SessionModeState {
  currentModeId:  SessionModeId (string, required)
  availableModes: []SessionMode (required)
  _meta:          map[string]any (optional)
}

SessionMode {
  id:          SessionModeId (string, required)
  name:        string (required)
  description: string (optional)
  _meta:       map[string]any (optional)
}

SessionModelState {
  currentModelId:  ModelId (string, required)
  availableModels: []ModelInfo (required)
  _meta:           map[string]any (optional)
}
```

### McpServer (discriminated union on `type`)

**Stdio (no `type` field -- default):**
```json
{
  "name": "filesystem",
  "command": "/path/to/mcp-server",
  "args": ["--stdio"],
  "env": [{"name": "API_KEY", "value": "secret123"}]
}
```

```
McpServerStdio {
  name:    string (required)
  command: string (required) -- absolute path
  args:    []string (required)
  env:     []EnvVariable (optional)
  _meta:   map[string]any (optional)
}
```

**HTTP (`type: "http"`):**
```json
{
  "type": "http",
  "name": "api-server",
  "url": "https://api.example.com/mcp",
  "headers": [{"name": "Authorization", "value": "Bearer token123"}]
}
```

```
McpServerHttp {
  type:    "http" (required, const)
  name:    string (required)
  url:     string (required)
  headers: []HttpHeader (required)
  _meta:   map[string]any (optional)
}
```

**SSE (`type: "sse"`, deprecated):**
```
McpServerSse {
  type:    "sse" (required, const)
  name:    string (required)
  url:     string (required)
  headers: []HttpHeader (required)
  _meta:   map[string]any (optional)
}
```

**Helper types:**
```
EnvVariable { name: string, value: string }
HttpHeader  { name: string, value: string }
```

### Method: `session/load`

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "session/load",
  "params": {
    "sessionId": "sess_789xyz",
    "cwd": "/home/user/project",
    "mcpServers": []
  }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "modes": { ... },
    "configOptions": [ ... ],
    "models": { ... }
  }
}
```

```
LoadSessionRequest {
  sessionId:  SessionId (string, required)
  cwd:        string (required)
  mcpServers: []McpServer (required)
  _meta:      map[string]any (optional)
}

LoadSessionResponse {
  modes:         SessionModeState (optional)
  configOptions: []SessionConfigOption (optional)
  models:        SessionModelState (optional)
  _meta:         map[string]any (optional)
}
```

**Important**: When loading a session, the agent replays conversation history via `session/update` notifications (`user_message_chunk`, `agent_message_chunk`, `tool_call`, etc.) BEFORE responding to the load request.

**Capability check**: Client MUST verify `agentCapabilities.loadSession == true` before calling.

---

## 6. Prompt Turn Lifecycle

### Method: `session/prompt`

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "session/prompt",
  "params": {
    "sessionId": "sess_abc123def456",
    "prompt": [
      {
        "type": "text",
        "text": "Can you analyze this code?"
      },
      {
        "type": "resource",
        "resource": {
          "uri": "file:///home/user/main.py",
          "mimeType": "text/x-python",
          "text": "def hello():\n    print('Hello')"
        }
      }
    ]
  }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": {
    "stopReason": "end_turn",
    "usage": {
      "inputTokens": 150,
      "outputTokens": 300,
      "totalTokens": 450,
      "thoughtTokens": 0,
      "cachedReadTokens": 50,
      "cachedWriteTokens": 0
    }
  }
}
```

### Type Definitions

```
PromptRequest {
  sessionId: SessionId (string, required)
  prompt:    []ContentBlock (required) -- user message content
  _meta:     map[string]any (optional)
}

PromptResponse {
  stopReason: StopReason (string, required)
  usage:      Usage (optional)
  _meta:      map[string]any (optional)
}

StopReason = "end_turn" | "max_tokens" | "max_turn_requests" | "refusal" | "cancelled"

Usage {
  inputTokens:      int (required)
  outputTokens:     int (required)
  totalTokens:      int (required)
  thoughtTokens:    int (optional)
  cachedReadTokens: int (optional)
  cachedWriteTokens: int (optional)
}

Cost {
  amount:   float64 (required)
  currency: string (required)
}
```

### Lifecycle Sequence

```
Client                          Agent
  |                               |
  |--- session/prompt ----------->|
  |                               |--- LLM processing
  |<-- session/update (plan) -----|
  |<-- session/update (agent_message_chunk) ---|
  |<-- session/update (tool_call) -------------|
  |<-- session/request_permission -------------|
  |--- permission response ------>|
  |<-- session/update (tool_call_update: in_progress) ---|
  |<-- session/update (tool_call_update: completed) -----|
  |<-- session/update (agent_message_chunk) -------------|
  |<-- session/prompt response ---|
  |                               |
```

### StopReason Values

| Value               | Meaning |
|---------------------|---------|
| `end_turn`          | LLM completed without requesting more tools |
| `max_tokens`        | Token limit reached |
| `max_turn_requests` | Max model requests in single turn exceeded |
| `refusal`           | Agent refuses to continue |
| `cancelled`         | Client cancelled via session/cancel |

### Cancellation: `session/cancel` (Notification)

```json
{
  "jsonrpc": "2.0",
  "method": "session/cancel",
  "params": {
    "sessionId": "sess_abc123def456"
  }
}
```

```
CancelNotification {
  sessionId: SessionId (string, required)
  _meta:     map[string]any (optional)
}
```

**Client responsibilities on cancel:**
- Preemptively mark all non-finished tool calls as cancelled
- Respond to all pending `session/request_permission` requests with `{ "outcome": { "outcome": "cancelled" } }`

**Agent responsibilities on cancel:**
- Stop all LLM requests and tool invocations ASAP
- Catch errors from aborted operations
- Send any remaining session/update notifications
- Respond to original session/prompt with `stopReason: "cancelled"`

---

## 7. Session Updates -- Complete Catalog

All session updates are sent as `session/update` notifications from agent to client:

```json
{
  "jsonrpc": "2.0",
  "method": "session/update",
  "params": {
    "sessionId": "...",
    "update": {
      "sessionUpdate": "<discriminator_value>",
      ...fields...
    }
  }
}
```

```
SessionNotification {
  sessionId: SessionId (string, required)
  update:    SessionUpdate (required) -- discriminated union on "sessionUpdate"
  _meta:     map[string]any (optional)
}
```

### SessionUpdate Discriminated Union

The `sessionUpdate` field is the discriminator. All 11 variants:

| sessionUpdate value          | Payload Type             | Description |
|------------------------------|--------------------------|-------------|
| `user_message_chunk`         | ContentChunk             | User message content (during session/load replay) |
| `agent_message_chunk`        | ContentChunk             | Agent text output from LLM |
| `agent_thought_chunk`        | ContentChunk             | Agent thinking/reasoning output |
| `tool_call`                  | ToolCall                 | New tool invocation created |
| `tool_call_update`           | ToolCallUpdate           | Status/content update to existing tool call |
| `plan`                       | Plan                     | Agent's execution plan |
| `available_commands_update`  | AvailableCommandsUpdate  | Available slash commands changed |
| `current_mode_update`        | CurrentModeUpdate        | Agent switched operating mode |
| `config_option_update`       | ConfigOptionUpdate       | Config options changed |
| `session_info_update`        | SessionInfoUpdate        | Session metadata changed |
| `usage_update`               | UsageUpdate              | Token/cost usage updated |

### 7.1 user_message_chunk / agent_message_chunk / agent_thought_chunk

All three share the same shape -- `ContentChunk`:

```json
{
  "sessionUpdate": "agent_message_chunk",
  "content": {
    "type": "text",
    "text": "The capital of France is Paris."
  }
}
```

```
ContentChunk {
  content: ContentBlock (required) -- see Content Blocks section
  _meta:   map[string]any (optional)
}
```

### 7.2 tool_call

```json
{
  "sessionUpdate": "tool_call",
  "toolCallId": "call_001",
  "title": "Reading configuration file",
  "kind": "read",
  "status": "pending",
  "locations": [{"path": "/home/user/config.json", "line": 1}],
  "content": [],
  "rawInput": {"path": "/home/user/config.json"}
}
```

```
ToolCall {
  toolCallId: ToolCallId (string, required)
  title:      string (required)
  kind:       ToolKind (optional)
  status:     ToolCallStatus (optional, defaults to "pending")
  content:    []ToolCallContent (optional)
  locations:  []ToolCallLocation (optional)
  rawInput:   any (optional) -- raw tool input parameters
  rawOutput:  any (optional)
  _meta:      map[string]any (optional)
}
```

### 7.3 tool_call_update

```json
{
  "sessionUpdate": "tool_call_update",
  "toolCallId": "call_001",
  "status": "completed",
  "content": [
    {
      "type": "content",
      "content": { "type": "text", "text": "Found 3 files..." }
    }
  ]
}
```

```
ToolCallUpdate {
  toolCallId: ToolCallId (string, required)
  title:      string (optional, nullable)
  kind:       ToolKind (optional, nullable)
  status:     ToolCallStatus (optional, nullable)
  content:    []ToolCallContent (optional, nullable)
  locations:  []ToolCallLocation (optional, nullable)
  rawInput:   any (optional)
  rawOutput:  any (optional)
  _meta:      map[string]any (optional)
}
```

**Key difference from ToolCall**: All fields except `toolCallId` are optional (partial update semantics).

### 7.4 plan

```json
{
  "sessionUpdate": "plan",
  "entries": [
    {"content": "Analyze codebase", "priority": "high", "status": "pending"},
    {"content": "Create tests", "priority": "medium", "status": "pending"}
  ]
}
```

See [Section 13: Agent Plan](#13-agent-plan).

### 7.5 available_commands_update

```json
{
  "sessionUpdate": "available_commands_update",
  "availableCommands": [
    {"name": "web", "description": "Search the web", "input": {"hint": "query"}},
    {"name": "test", "description": "Run tests"}
  ]
}
```

See [Section 12: Slash Commands](#12-slash-commands).

### 7.6 current_mode_update

```json
{
  "sessionUpdate": "current_mode_update",
  "currentModeId": "code"
}
```

```
CurrentModeUpdate {
  currentModeId: SessionModeId (string, required)
  _meta:         map[string]any (optional)
}
```

### 7.7 config_option_update

```json
{
  "sessionUpdate": "config_option_update",
  "configOptions": [...]
}
```

```
ConfigOptionUpdate {
  configOptions: []SessionConfigOption (required)
  _meta:         map[string]any (optional)
}
```

### 7.8 session_info_update

```json
{
  "sessionUpdate": "session_info_update",
  "title": "Analyzing Python Code",
  "updatedAt": "2026-02-24T10:30:00Z"
}
```

```
SessionInfoUpdate {
  title:     string (optional, nullable)
  updatedAt: string (optional, nullable) -- ISO 8601
  _meta:     map[string]any (optional)
}
```

### 7.9 usage_update

```json
{
  "sessionUpdate": "usage_update",
  "size": 200000,
  "used": 45000,
  "cost": {"amount": 0.05, "currency": "USD"}
}
```

```
UsageUpdate {
  size: int (required) -- total context window size
  used: int (required) -- tokens used so far
  cost: Cost (optional)
  _meta: map[string]any (optional)
}
```

---

## 8. Tool Calls

### Enums

```
ToolKind = "read" | "edit" | "delete" | "move" | "search"
         | "execute" | "think" | "fetch" | "switch_mode" | "other"

ToolCallStatus = "pending" | "in_progress" | "completed" | "failed"
```

### ToolCallContent (discriminated union on `type`)

**Regular content:**
```json
{"type": "content", "content": {"type": "text", "text": "Analysis complete."}}
```

```
ToolCallContentContent {
  type:    "content" (const)
  content: ContentBlock (required)
  _meta:   map[string]any (optional)
}
```

**Diff:**
```json
{
  "type": "diff",
  "path": "/home/user/config.json",
  "oldText": "{\"debug\": false}",
  "newText": "{\"debug\": true}"
}
```

```
ToolCallContentDiff {
  type:    "diff" (const)
  path:    string (required) -- absolute file path
  oldText: string (optional, null for new files)
  newText: string (required)
  _meta:   map[string]any (optional)
}
```

**Terminal reference:**
```json
{"type": "terminal", "terminalId": "term_xyz789"}
```

```
ToolCallContentTerminal {
  type:       "terminal" (const)
  terminalId: string (required)
  _meta:      map[string]any (optional)
}
```

### ToolCallLocation

```
ToolCallLocation {
  path: string (required) -- absolute file path
  line: int (optional)
  _meta: map[string]any (optional)
}
```

---

## 9. Permission System

### Method: `session/request_permission` (Agent -> Client)

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "method": "session/request_permission",
  "params": {
    "sessionId": "sess_abc123def456",
    "toolCall": {
      "toolCallId": "call_001",
      "title": "Write to config.json",
      "kind": "edit",
      "status": "pending"
    },
    "options": [
      {"optionId": "allow-once", "name": "Allow once", "kind": "allow_once"},
      {"optionId": "allow-always", "name": "Always allow", "kind": "allow_always"},
      {"optionId": "reject-once", "name": "Reject", "kind": "reject_once"},
      {"optionId": "reject-always", "name": "Always reject", "kind": "reject_always"}
    ]
  }
}
```

**Response (selected):**
```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "result": {
    "outcome": {
      "outcome": "selected",
      "optionId": "allow-once"
    }
  }
}
```

**Response (cancelled -- e.g. on session/cancel):**
```json
{
  "jsonrpc": "2.0",
  "id": 5,
  "result": {
    "outcome": {
      "outcome": "cancelled"
    }
  }
}
```

### Type Definitions

```
RequestPermissionRequest {
  sessionId: SessionId (string, required)
  toolCall:  ToolCallUpdate (required) -- describes the tool needing permission
  options:   []PermissionOption (required)
  _meta:     map[string]any (optional)
}

RequestPermissionResponse {
  outcome: RequestPermissionOutcome (required)
  _meta:   map[string]any (optional)
}

RequestPermissionOutcome =
    { outcome: "cancelled" }
  | { outcome: "selected", optionId: PermissionOptionId }

PermissionOption {
  optionId: PermissionOptionId (string, required)
  name:     string (required)
  kind:     PermissionOptionKind (required)
  _meta:    map[string]any (optional)
}

PermissionOptionKind = "allow_once" | "allow_always" | "reject_once" | "reject_always"
```

### HITL (Human-in-the-Loop) Pattern

The ACP permission system IS the HITL mechanism. Every tool invocation can be gated:

1. Agent creates tool_call update with `status: "pending"`
2. Agent sends `session/request_permission` with the tool call info and option choices
3. Client presents options to user, responds with selection
4. Agent proceeds based on outcome:
   - `allow_once`: execute this tool call only
   - `allow_always`: execute and don't ask again for this tool kind
   - `reject_once`: skip this tool call
   - `reject_always`: skip and don't ask again for this tool kind
   - `cancelled`: user cancelled the entire turn

---

## 10. Session Modes

### Method: `session/set_mode` (Client -> Agent)

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "session/set_mode",
  "params": {
    "sessionId": "sess_abc123def456",
    "modeId": "code"
  }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 2,
  "result": null
}
```

```
SetSessionModeRequest {
  sessionId: SessionId (string, required)
  modeId:    SessionModeId (string, required)
  _meta:     map[string]any (optional)
}
```

**Note**: Session Config Options (section 11) are the newer, more flexible approach. Dedicated mode methods remain for backwards compatibility. Config options with `category: "mode"` supersede modes for clients that support config options.

---

## 11. Session Config Options

### Method: `session/set_config_option` (Client -> Agent)

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "session/set_config_option",
  "params": {
    "sessionId": "sess_abc123def456",
    "configId": "model",
    "value": "claude-opus-4-20250514"
  }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "configOptions": [
      {
        "id": "model",
        "name": "Model",
        "category": "model",
        "type": "select",
        "currentValue": "claude-opus-4-20250514",
        "options": [...]
      }
    ]
  }
}
```

### Type Definitions

```
SetSessionConfigOptionRequest {
  sessionId: SessionId (string, required)
  configId:  SessionConfigId (string, required)
  value:     SessionConfigValueId (string, required)
  _meta:     map[string]any (optional)
}

SetSessionConfigOptionResponse {
  configOptions: []SessionConfigOption (required)
  _meta:         map[string]any (optional)
}

SessionConfigOption {
  id:           SessionConfigId (string, required)
  name:         string (required) -- human-readable label
  description:  string (optional)
  category:     SessionConfigOptionCategory (optional)
  type:         "select" (required, only supported type currently)
  currentValue: SessionConfigValueId (string, required)
  options:      SessionConfigSelectOptions (required)
  _meta:        map[string]any (optional)
}

SessionConfigSelectOptions = []SessionConfigSelectOption | []SessionConfigSelectGroup

SessionConfigSelectOption {
  value:       SessionConfigValueId (string, required)
  name:        string (required)
  description: string (optional)
  _meta:       map[string]any (optional)
}

SessionConfigSelectGroup {
  group:   SessionConfigGroupId (string, required)
  name:    string (required)
  options: []SessionConfigSelectOption (required)
  _meta:   map[string]any (optional)
}

SessionConfigOptionCategory = "mode" | "model" | "thought_level" | string
  -- Custom categories MUST be prefixed with underscore (e.g., "_custom")
  -- Categories are for UX only, MUST NOT be required for correctness
```

### Key Rules

- Agents MUST always provide a default value for every config option
- Agents respond with COMPLETE config state (may reflect dependent changes)
- Agent can also push changes via `config_option_update` session update
- Config options supersede Session Modes API for clients supporting them

---

## 12. Slash Commands

Slash commands are delivered as regular `session/prompt` text messages prefixed with `/`.

### Available Commands Update

```json
{
  "sessionUpdate": "available_commands_update",
  "availableCommands": [
    {
      "name": "web",
      "description": "Search the web for information",
      "input": {"hint": "query to search for"}
    },
    {
      "name": "test",
      "description": "Run tests for the current project"
    }
  ]
}
```

```
AvailableCommandsUpdate {
  availableCommands: []AvailableCommand (required)
  _meta:             map[string]any (optional)
}

AvailableCommand {
  name:        string (required) -- command identifier (no leading slash)
  description: string (required) -- human-readable explanation
  input:       AvailableCommandInput (optional)
  _meta:       map[string]any (optional)
}

AvailableCommandInput {
  hint: string (required) -- placeholder text for input field
  _meta: map[string]any (optional)
}
```

### Invoking Commands

Just send as a regular prompt:
```json
{
  "method": "session/prompt",
  "params": {
    "sessionId": "...",
    "prompt": [{"type": "text", "text": "/web agent client protocol"}]
  }
}
```

---

## 13. Agent Plan

```json
{
  "sessionUpdate": "plan",
  "entries": [
    {"content": "Analyze existing codebase", "priority": "high", "status": "in_progress"},
    {"content": "Identify refactoring needs", "priority": "high", "status": "pending"},
    {"content": "Create unit tests", "priority": "medium", "status": "pending"}
  ]
}
```

```
Plan {
  entries: []PlanEntry (required)
  _meta:   map[string]any (optional)
}

PlanEntry {
  content:  string (required) -- human-readable description
  priority: PlanEntryPriority (required)
  status:   PlanEntryStatus (required)
  _meta:    map[string]any (optional)
}

PlanEntryPriority = "high" | "medium" | "low"
PlanEntryStatus   = "pending" | "in_progress" | "completed"
```

**Key rule**: Each plan update sends the COMPLETE plan. Clients replace the entire plan on each update (not incremental).

---

## 14. File System Operations

### Capability Check

Client must advertise `fs.readTextFile` and/or `fs.writeTextFile` in `clientCapabilities`. Agent MUST NOT call these methods if capability is absent.

### Method: `fs/read_text_file` (Agent -> Client)

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "method": "fs/read_text_file",
  "params": {
    "sessionId": "sess_abc123def456",
    "path": "/home/user/project/main.py",
    "line": 1,
    "limit": 100
  }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 10,
  "result": {
    "content": "def hello():\n    print('Hello')"
  }
}
```

```
ReadTextFileRequest {
  sessionId: SessionId (string, required)
  path:      string (required) -- absolute path
  line:      int (optional) -- starting line, 1-based
  limit:     int (optional) -- max lines to read
  _meta:     map[string]any (optional)
}

ReadTextFileResponse {
  content: string (required)
  _meta:   map[string]any (optional)
}
```

### Method: `fs/write_text_file` (Agent -> Client)

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 11,
  "method": "fs/write_text_file",
  "params": {
    "sessionId": "sess_abc123def456",
    "path": "/home/user/project/output.txt",
    "content": "Hello, world!"
  }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 11,
  "result": {}
}
```

```
WriteTextFileRequest {
  sessionId: SessionId (string, required)
  path:      string (required) -- absolute path
  content:   string (required) -- text to write
  _meta:     map[string]any (optional)
}

WriteTextFileResponse {
  _meta: map[string]any (optional)
}
```

**Note**: Client creates parent directories if missing. The path MUST be absolute. fs/read_text_file may include unsaved editor buffer state (not just disk content).

---

## 15. Terminal Operations

### Capability Check

Client must advertise `terminal: true` in `clientCapabilities`. Agent MUST verify before calling any terminal method.

### Method: `terminal/create` (Agent -> Client)

**Request:**
```json
{
  "jsonrpc": "2.0",
  "id": 20,
  "method": "terminal/create",
  "params": {
    "sessionId": "sess_abc123def456",
    "command": "npm",
    "args": ["test"],
    "env": [{"name": "NODE_ENV", "value": "test"}],
    "cwd": "/home/user/project",
    "outputByteLimit": 1048576
  }
}
```

**Response:**
```json
{
  "jsonrpc": "2.0",
  "id": 20,
  "result": {
    "terminalId": "term_abc123"
  }
}
```

```
CreateTerminalRequest {
  sessionId:       SessionId (string, required)
  command:         string (required)
  args:            []string (optional)
  env:             []EnvVariable (optional)
  cwd:             string (optional) -- absolute path
  outputByteLimit: int (optional) -- max output bytes to retain
  _meta:           map[string]any (optional)
}

CreateTerminalResponse {
  terminalId: string (required)
  _meta:      map[string]any (optional)
}
```

### Method: `terminal/output` (Agent -> Client)

```
TerminalOutputRequest {
  sessionId:  SessionId (string, required)
  terminalId: string (required)
  _meta:      map[string]any (optional)
}

TerminalOutputResponse {
  output:     string (required) -- captured output
  truncated:  bool (required) -- whether output was truncated
  exitStatus: TerminalExitStatus (optional) -- present if command exited
  _meta:      map[string]any (optional)
}

TerminalExitStatus {
  exitCode: int (optional)
  signal:   string (optional)
}
```

### Method: `terminal/wait_for_exit` (Agent -> Client)

```
WaitForTerminalExitRequest {
  sessionId:  SessionId (string, required)
  terminalId: string (required)
  _meta:      map[string]any (optional)
}

WaitForTerminalExitResponse {
  exitCode: int (optional)
  signal:   string (optional)
  _meta:    map[string]any (optional)
}
```

### Method: `terminal/kill` (Agent -> Client)

Terminates the command without releasing the terminal.

```
KillTerminalCommandRequest {
  sessionId:  SessionId (string, required)
  terminalId: string (required)
  _meta:      map[string]any (optional)
}
```

### Method: `terminal/release` (Agent -> Client)

Kills command if running AND releases all resources. Terminal ID becomes invalid.

```
ReleaseTerminalRequest {
  sessionId:  SessionId (string, required)
  terminalId: string (required)
  _meta:      map[string]any (optional)
}
```

---

## 16. Content Blocks

ContentBlock is a discriminated union on the `type` field. Used in prompts, agent messages, and tool call content.

### All agents MUST support: `text`, `resource_link`

### 16.1 Text

```json
{"type": "text", "text": "Hello, world!", "annotations": null}
```

```
TextContent {
  type:        "text" (const)
  text:        string (required)
  annotations: Annotations (optional)
}
```

### 16.2 Image (requires `promptCapabilities.image`)

```json
{
  "type": "image",
  "mimeType": "image/png",
  "data": "iVBORw0KGgoAAAANSUhEUg...",
  "uri": "file:///path/to/image.png"
}
```

```
ImageContent {
  type:        "image" (const)
  data:        string (required) -- base64 encoded
  mimeType:    string (required) -- e.g. "image/png", "image/jpeg"
  uri:         string (optional)
  annotations: Annotations (optional)
}
```

### 16.3 Audio (requires `promptCapabilities.audio`)

```json
{
  "type": "audio",
  "mimeType": "audio/wav",
  "data": "UklGRiQAAABXQVZF..."
}
```

```
AudioContent {
  type:        "audio" (const)
  data:        string (required) -- base64 encoded
  mimeType:    string (required)
  annotations: Annotations (optional)
}
```

### 16.4 Resource Link (baseline support required)

```json
{
  "type": "resource_link",
  "uri": "file:///home/user/doc.pdf",
  "name": "doc.pdf",
  "mimeType": "application/pdf",
  "title": "Project Documentation",
  "description": "Main documentation file",
  "size": 1024000
}
```

```
ResourceLink {
  type:        "resource_link" (const)
  uri:         string (required)
  name:        string (required)
  mimeType:    string (optional)
  title:       string (optional)
  description: string (optional)
  size:        int (optional) -- bytes
  annotations: Annotations (optional)
}
```

### 16.5 Embedded Resource (requires `promptCapabilities.embeddedContext`)

```json
{
  "type": "resource",
  "resource": {
    "uri": "file:///home/user/script.py",
    "mimeType": "text/x-python",
    "text": "def hello():\n    print('Hello')"
  }
}
```

Text variant:
```
EmbeddedResource {
  type:     "resource" (const)
  resource: TextResourceContents | BlobResourceContents (required)
  annotations: Annotations (optional)
}

TextResourceContents {
  uri:      string (required)
  text:     string (required)
  mimeType: string (optional)
}

BlobResourceContents {
  uri:      string (required)
  blob:     string (required) -- base64 encoded
  mimeType: string (optional)
}
```

### Annotations

```
Annotations {
  audience:     []Role (optional) -- "assistant" | "user"
  lastModified: string (optional) -- ISO 8601 timestamp
  priority:     float64 (optional) -- 0.0 to 1.0
  _meta:        map[string]any (optional)
}

Role = "assistant" | "user"
```

**Design note**: ACP ContentBlock structure matches MCP (Model Context Protocol) types. Agents can forward MCP tool outputs directly without transformation.

---

## 17. Error Handling

### JSON-RPC Error Codes

| Code    | Name              | Description |
|---------|-------------------|-------------|
| -32700  | Parse Error       | Invalid JSON |
| -32600  | Invalid Request   | Not a valid JSON-RPC request |
| -32601  | Method Not Found  | Method does not exist |
| -32602  | Invalid Params    | Invalid method parameters |
| -32603  | Internal Error    | Internal JSON-RPC error |
| -32800  | Request Cancelled | Request was cancelled (unstable) |
| -32000  | Auth Required     | Authentication required |
| -32002  | Resource Not Found| Requested resource does not exist |

### Error Response Format

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "error": {
    "code": -32601,
    "message": "Method not found",
    "data": null
  }
}
```

```
Error {
  code:    ErrorCode (int, required)
  message: string (required)
  data:    any (optional)
}
```

### Client Error Handling Guidance

- Standard JSON-RPC errors (-32700 to -32603): protocol-level issues, usually fatal
- Auth Required (-32000): re-trigger authentication flow
- Resource Not Found (-32002): specific resource unavailable, may include URI in `data`
- Request Cancelled (-32800): operation was cancelled, non-fatal
- Unknown extension requests: respond with -32601 (Method not found)
- Unknown notifications: silently ignore

---

## 18. Extensibility

### Three Extension Mechanisms

#### 1. `_meta` Field

Every protocol type supports `_meta: { [key: string]: unknown }` for custom metadata.

```json
{
  "method": "session/prompt",
  "params": {
    "sessionId": "...",
    "prompt": [...],
    "_meta": {
      "traceparent": "00-80e1afed...",
      "zed.dev/debugMode": true
    }
  }
}
```

**Rules:**
- Root-level `_meta` keys SHOULD reserve: `traceparent`, `tracestate`, `baggage` (W3C trace context)
- Implementations MUST NOT add custom fields at type root level -- all names reserved for future spec versions
- Use `_meta` for ALL custom data

#### 2. Underscore-Prefixed Methods

Custom methods MUST start with `_`:

```json
{"method": "_zed.dev/workspace/buffers", "params": {"language": "rust"}}
```

- Requests: MUST include `id`, MUST receive response
- Notifications: no `id`, implementations SHOULD ignore unrecognized notifications
- Custom methods SHOULD be advertised in capabilities via `_meta`

#### 3. Capability Advertisement

```json
{
  "agentCapabilities": {
    "loadSession": true,
    "_meta": {
      "zed.dev": {
        "workspace": true,
        "fileNotifications": true
      }
    }
  }
}
```

---

## 19. Complete Type Reference

### All String Alias Types

```
SessionId              = string
SessionModeId          = string
SessionConfigId        = string
SessionConfigValueId   = string
SessionConfigGroupId   = string
ModelId                = string
ToolCallId             = string
PermissionOptionId     = string
AuthMethodId           = string
ProtocolVersion        = int (currently 1)
```

### All Enum Types

```
StopReason             = "end_turn" | "max_tokens" | "max_turn_requests" | "refusal" | "cancelled"
ToolKind               = "read" | "edit" | "delete" | "move" | "search" | "execute" | "think" | "fetch" | "switch_mode" | "other"
ToolCallStatus         = "pending" | "in_progress" | "completed" | "failed"
PermissionOptionKind   = "allow_once" | "allow_always" | "reject_once" | "reject_always"
PlanEntryPriority      = "high" | "medium" | "low"
PlanEntryStatus        = "pending" | "in_progress" | "completed"
Role                   = "assistant" | "user"
SessionConfigOptionCategory = "mode" | "model" | "thought_level" | string (custom: "_"-prefixed)
```

### All Discriminated Unions

| Type                   | Discriminator      | Variants |
|------------------------|--------------------|----------|
| SessionUpdate          | `sessionUpdate`    | user_message_chunk, agent_message_chunk, agent_thought_chunk, tool_call, tool_call_update, plan, available_commands_update, current_mode_update, config_option_update, session_info_update, usage_update |
| ContentBlock           | `type`             | text, image, audio, resource_link, resource |
| ToolCallContent        | `type`             | content, diff, terminal |
| McpServer              | `type` (optional)  | (none)=stdio, http, sse |
| RequestPermissionOutcome | `outcome`        | cancelled, selected |

---

## 20. Real-World Implementations

### Agents Supporting ACP

28+ agents support ACP as of Feb 2026, including:
- **Claude Code** -- via Zed's SDK adapter (github.com/zed-industries/claude-agent-acp)
- **Gemini CLI** -- native ACP support (referenced as production example by TS SDK)
- **OpenCode** (by SST) -- native ACP support
- **GitHub Copilot** -- public preview since Jan 2026
- **Mistral Vibe**, **Qwen Code**, and many others

### Clients Supporting ACP

- **Zed** -- external agent capabilities (primary driver of the spec)
- **VS Code** -- via ACP Client extension
- **JetBrains IDEs** -- built-in ACP support
- **Neovim** -- CodeCompanion, agentic.nvim, avante.nvim plugins
- **Emacs** -- agent-shell.el
- **acpx** -- CLI client
- Many more (Obsidian, Chrome, standalone apps)

### Go Community SDK: github.com/ironpark/acp-go

- Unofficial but comprehensive
- Generated types from official schema
- Provides `AgentSideConnection` and `ClientSideConnection`
- `Connection` type handles JSON-RPC multiplexing over stdio
- 57+ struct types, discriminated unions via custom JSON marshaling
- MIT licensed

### TypeScript SDK: @agentclientprotocol/sdk (Official)

- Primary reference implementation
- v0.14.1 (latest)
- `AgentSideConnection` and `ClientSideConnection` classes
- Zod validation for all incoming parameters
- Full JSON-RPC 2.0 compliance
- Extension method/notification support

### Zed Editor Connection Flow

Zed spawns the agent as a subprocess, communicates via stdin/stdout. Multiple concurrent sessions on a single connection. Uses `_meta` for custom Zed-specific capabilities like workspace buffers and file notifications.

### OpenCode ACP

OpenCode (by SST) supports ACP natively. It's one of the production agents listed in the spec. The protocol is the standard ACP protocol -- no known quirks beyond what the spec defines. OpenCode previously used its own CLI JSON output format, but now supports ACP as a first-class protocol.

---

## 21. Delta from Current agentrun/engine/acp

The existing `engine/acp/protocol.go` diverges significantly from the actual ACP spec:

### Critical Differences

| Aspect | Current Code | Actual ACP Spec |
|--------|-------------|-----------------|
| Protocol version | `"0.1"` (string) | `1` (integer) |
| Initialize params | `ClientInfo`, `Capabilities` | `clientInfo`, `clientCapabilities`, `protocolVersion` |
| Capabilities | `fileOperations`, `terminalExecution` (bools) | `fs: {readTextFile, writeTextFile}`, `terminal` (bool) |
| Session/new params | `config: {systemPrompt, model, mode, thinking}` | `cwd` (string, required), `mcpServers` (required) |
| Session/new response | `sessionId` only | `sessionId`, `modes`, `configOptions`, `models` |
| Prompt params | `messages: [{role, content}]` | `prompt: []ContentBlock` (no role field) |
| Prompt response | `stopReason` | `stopReason`, `usage` |
| Update notification | `type` + `data` (custom) | `sessionUpdate` discriminator on update object |
| Update types | thinking-delta, text-delta, tool-use, etc. (custom) | agent_message_chunk, tool_call, tool_call_update, etc. |
| Permission | `toolName`, `description` -> `approved` bool | `toolCall` (ToolCallUpdate), `options` (PermissionOption[]) -> `outcome` |

### What Needs to Change

1. **Protocol types**: Complete rewrite of `protocol.go` to match ACP spec types
2. **Update parsing**: Rewrite `update.go` to use `sessionUpdate` discriminator with proper ACP update types
3. **Permission handler**: Change from simple approve/reject to option-based selection
4. **Session setup**: Add `cwd` and `mcpServers` to session/new; handle `modes`, `configOptions`, `models` in response
5. **Content blocks**: Implement ContentBlock discriminated union for prompts
6. **Initialize**: Fix protocol version to integer `1`, restructure capabilities

---

## 22. Ambiguities and Spec Gaps

### Confirmed Gaps

1. **Error documentation**: The error protocol page says "Documentation coming soon" -- error handling details come only from the Rust SDK source and JSON schema, not official docs.

2. **Authentication flow**: The `authenticate` method is defined, but detailed auth method types (`agent`, `env_var`, `terminal`) are in an RFD (Request for Discussion), not the stable spec. The base `AuthMethod` type only has `id`, `name`, `description`.

3. **session/load response body**: The spec is unclear whether `session/load` returns `null` or an object with `modes`/`configOptions`/`models`. The TypeScript SDK types show it returning `LoadSessionResponse` with optional modes/configOptions/models. The spec website example shows `result: null`.

4. **Request Cancelled error code (-32800)**: Marked as "unstable" in the Rust SDK. Only available behind a feature flag. May not be implemented by all agents.

5. **SessionModelState**: Present in TypeScript SDK types but NOT mentioned in the spec documentation pages. Appears in `NewSessionResponse` and `LoadSessionResponse`. Likely a newer addition.

6. **SessionInfoUpdate and UsageUpdate**: Added in v0.10.3+ and v0.10.8 respectively. Relatively new session update types. Not all agents may implement them.

7. **ConfigOption options field**: TypeScript SDK defines as `SessionConfigSelectOptions = Array<SessionConfigSelectOption> | Array<SessionConfigSelectGroup>` -- it's EITHER flat options OR grouped options, not mixed.

8. **McpServer stdio type**: Stdio transport has NO `type` field (unlike http/sse). This makes the discriminator implicit -- if `type` is absent, it's stdio.

9. **WriteTextFileResponse**: TypeScript SDK shows empty object `{}`. Go SDK has no response type defined. The spec says `result: null`. Implementations may vary between `null` and `{}`.

10. **StopReason schema discrepancy**: The schema.json from the official repo showed different values (`completed, cancelled, rate_limited, tool_call, request_permission`) than the actual spec docs and TypeScript SDK (`end_turn, max_tokens, max_turn_requests, refusal, cancelled`). The TypeScript SDK and Go community SDK values are authoritative -- the schema.json fetch was likely truncated or from an older/unstable version.

11. **Filesystem page 404**: The URL `/protocol/filesystem` returns 404 -- actual path is `/protocol/file-system` (hyphenated). Minor web routing issue.

### Open Questions

- How do agents handle multiple concurrent sessions on a single stdio connection? (Spec says "Multiple concurrent sessions operate within a single connection" but the mechanics of concurrent prompt turns aren't detailed.)
- What happens if a client sends `session/prompt` while a previous prompt is still being processed? (The spec doesn't explicitly address this.)
- Should `_meta` be preserved/forwarded by intermediary implementations? (W3C trace context suggests yes, but it's only SHOULD-level.)

---

## Appendix A: JSON-RPC 2.0 Quick Reference

```
Request:      { "jsonrpc": "2.0", "id": <int|string>, "method": "<string>", "params": <object> }
Response:     { "jsonrpc": "2.0", "id": <int|string>, "result": <any> }
Error:        { "jsonrpc": "2.0", "id": <int|string>, "error": { "code": <int>, "message": <string>, "data": <any> } }
Notification: { "jsonrpc": "2.0", "method": "<string>", "params": <object> }
```

- Requests have `id` and expect a response
- Notifications have NO `id` and NO response
- `id` can be integer or string (ACP typically uses integer)
- `params` is always an object (not array) in ACP

## Appendix B: Method Name -> Request/Response Type Mapping

| Method                      | Direction | Request Type                   | Response Type                      |
|-----------------------------|-----------|--------------------------------|------------------------------------|
| `initialize`                | C->A      | InitializeRequest              | InitializeResponse                 |
| `authenticate`              | C->A      | AuthenticateRequest            | AuthenticateResponse (empty)       |
| `session/new`               | C->A      | NewSessionRequest              | NewSessionResponse                 |
| `session/load`              | C->A      | LoadSessionRequest             | LoadSessionResponse                |
| `session/prompt`            | C->A      | PromptRequest                  | PromptResponse                     |
| `session/cancel`            | C->A      | CancelNotification             | (notification, no response)        |
| `session/set_mode`          | C->A      | SetSessionModeRequest          | null                               |
| `session/set_config_option` | C->A      | SetSessionConfigOptionRequest  | SetSessionConfigOptionResponse     |
| `session/update`            | A->C      | SessionNotification            | (notification, no response)        |
| `session/request_permission`| A->C      | RequestPermissionRequest       | RequestPermissionResponse          |
| `fs/read_text_file`         | A->C      | ReadTextFileRequest            | ReadTextFileResponse               |
| `fs/write_text_file`        | A->C      | WriteTextFileRequest           | WriteTextFileResponse (empty)      |
| `terminal/create`           | A->C      | CreateTerminalRequest          | CreateTerminalResponse             |
| `terminal/output`           | A->C      | TerminalOutputRequest          | TerminalOutputResponse             |
| `terminal/wait_for_exit`    | A->C      | WaitForTerminalExitRequest     | WaitForTerminalExitResponse        |
| `terminal/kill`             | A->C      | KillTerminalCommandRequest     | null                               |
| `terminal/release`          | A->C      | ReleaseTerminalRequest         | null                               |

## Appendix C: Go Community SDK Type Mapping Quick Reference

From `github.com/ironpark/acp-go`:

```go
// Method name constants
var AgentMethods = struct {
    Initialize    string  // "initialize"
    Authenticate  string  // "authenticate"
    SessionNew    string  // "session/new"
    SessionLoad   string  // "session/load"
    SessionPrompt string  // "session/prompt"
    SessionCancel string  // "session/cancel"
    SessionSetMode string // "session/set_mode"
}{...}

var ClientMethods = struct {
    SessionUpdate          string  // "session/update"
    RequestPermission      string  // "session/request_permission"
    ReadTextFile           string  // "fs/read_text_file"
    WriteTextFile          string  // "fs/write_text_file"
    TerminalCreate         string  // "terminal/create"
    TerminalOutput         string  // "terminal/output"
    TerminalRelease        string  // "terminal/release"
    TerminalWaitForExit    string  // "terminal/wait_for_exit"
    TerminalKill           string  // "terminal/kill"
}{...}

// Key constants
const CurrentProtocolVersion ProtocolVersion = 1

// SessionUpdate discriminator values
"user_message_chunk"
"agent_message_chunk"
"agent_thought_chunk"
"tool_call"
"tool_call_update"
"plan"
"available_commands_update"
"current_mode_update"
"config_option_update"  // CONFIRMED: TypeScript SDK uses singular "config_option_update" (not plural)
"session_info_update"
"usage_update"
```
