package claude

import (
	"testing"

	"github.com/dmora/agentrun"
)

// --- ToolCall.ID + Message.Tools (multi-spawn capture) ---

func TestParseLine_ToolUseCapturesID(t *testing.T) {
	b := New()
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_abc","name":"Agent","input":{"subagent_type":"general-purpose"}}]},"parent_tool_use_id":null}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Tool == nil {
		t.Fatal("Tool should be set")
	}
	if msg.Tool.ID != "toolu_abc" {
		t.Errorf("Tool.ID = %q, want %q", msg.Tool.ID, "toolu_abc")
	}
	if msg.Tool.Name != "Agent" {
		t.Errorf("Tool.Name = %q, want %q", msg.Tool.Name, "Agent")
	}
	if len(msg.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(msg.Tools))
	}
	if msg.Tools[0] != msg.Tool {
		t.Error("Tools[0] should be the same pointer as Tool for a single tool_use")
	}
}

func TestParseLine_ParallelSpawnsCaptureAllTools(t *testing.T) {
	b := New()
	// One assistant message spawning three subagents at once — the headline
	// fan-out case. Tool (last-one-wins) must not hide the other two.
	line := `{"type":"assistant","message":{"content":[` +
		`{"type":"text","text":"launching three"},` +
		`{"type":"tool_use","id":"toolu_1","name":"Agent","input":{}},` +
		`{"type":"tool_use","id":"toolu_2","name":"Agent","input":{}},` +
		`{"type":"tool_use","id":"toolu_3","name":"Task","input":{}}` +
		`]},"parent_tool_use_id":null}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.Tools) != 3 {
		t.Fatalf("len(Tools) = %d, want 3", len(msg.Tools))
	}
	wantIDs := []string{"toolu_1", "toolu_2", "toolu_3"}
	for i, want := range wantIDs {
		if msg.Tools[i].ID != want {
			t.Errorf("Tools[%d].ID = %q, want %q", i, msg.Tools[i].ID, want)
		}
	}
	// Tool stays the LAST tool_use for backward compatibility.
	if msg.Tool == nil || msg.Tool.ID != "toolu_3" {
		t.Errorf("Tool = %+v, want last (toolu_3)", msg.Tool)
	}
	// Text content is still captured alongside the tools.
	if msg.Content != "launching three" {
		t.Errorf("Content = %q, want %q", msg.Content, "launching three")
	}
}

// --- ParentToolUseID stamping ---

func TestParseLine_ParentToolUseIDStamped(t *testing.T) {
	b := New()
	tests := []struct {
		name string
		line string
		want string
	}{
		{
			"parent-level (null)",
			`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]},"parent_tool_use_id":null}`,
			"",
		},
		{
			"sidechain (set)",
			`{"type":"assistant","message":{"content":[{"type":"text","text":"inner"}]},"parent_tool_use_id":"toolu_parent"}`,
			"toolu_parent",
		},
		{
			"absent",
			`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`,
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := b.ParseLine(tt.line)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if msg.ParentToolUseID != tt.want {
				t.Errorf("ParentToolUseID = %q, want %q", msg.ParentToolUseID, tt.want)
			}
		})
	}
}

// --- background_tasks_changed -> MessageBackgroundTasks ---

func TestParseLine_BackgroundTasksChanged_MixedKinds(t *testing.T) {
	b := New()
	// Real wire shape (testdata/subagent/bg-revive.jsonl:7): a snapshot with
	// one subagent (local_agent) and one interleaved background Bash
	// (local_bash) task.
	line := `{"type":"system","subtype":"background_tasks_changed","tasks":[` +
		`{"task_id":"a9ee0ce61d88b6ef6","task_type":"local_agent","description":"Sleep then write probe artifact"},` +
		`{"task_id":"bkft2fbrh","task_type":"local_bash","description":"Sleep for 45 seconds"}` +
		`],"session_id":"d82c6a90-8ec7-4ec6-841d-b5760160d564"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageBackgroundTasks {
		t.Fatalf("Type = %q, want %q", msg.Type, agentrun.MessageBackgroundTasks)
	}

	// Tasks (ADR-3): unconditional parse — BOTH entries present, kind-tagged,
	// in wire order. This is the fix: a bash-only snapshot used to be
	// indistinguishable from no work at all.
	wantTasks := []agentrun.BackgroundTask{
		{ID: "a9ee0ce61d88b6ef6", Kind: agentrun.BackgroundSubagent, Description: "Sleep then write probe artifact"},
		{ID: "bkft2fbrh", Kind: agentrun.BackgroundShell, Description: "Sleep for 45 seconds"},
	}
	if len(msg.Tasks) != len(wantTasks) {
		t.Fatalf("len(Tasks) = %d, want %d", len(msg.Tasks), len(wantTasks))
	}
	for i, want := range wantTasks {
		if msg.Tasks[i] != want {
			t.Errorf("Tasks[%d] = %+v, want %+v", i, msg.Tasks[i], want)
		}
	}

	// Tools (transitional — see parseBackgroundTasks): still subagent-only,
	// ID-only. The stage-3 tracker still reads this as its pending set.
	if len(msg.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1 (local_bash filtered out of Tools)", len(msg.Tools))
	}
	if msg.Tools[0].ID != "a9ee0ce61d88b6ef6" {
		t.Errorf("Tools[0].ID = %q, want %q", msg.Tools[0].ID, "a9ee0ce61d88b6ef6")
	}
}

func TestParseLine_BackgroundTasksChanged_EmptyIsNonNil(t *testing.T) {
	b := New()
	// Drained set, real wire shape (testdata/backgroundtask/streaming.jsonl:15).
	// Both Tools and Tasks must be non-nil-but-empty so the tracker treats
	// this as an authoritative "zero pending" snapshot, not "no snapshot".
	line := `{"type":"system","subtype":"background_tasks_changed","tasks":[],` +
		`"uuid":"a9a222f7-0845-4d73-84b0-28ab38fcd789","session_id":"67794e8a-a51c-477a-8018-188014895e61"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageBackgroundTasks {
		t.Fatalf("Type = %q, want %q", msg.Type, agentrun.MessageBackgroundTasks)
	}
	if msg.Tools == nil {
		t.Error("Tools must be non-nil (empty snapshot), got nil")
	}
	if len(msg.Tools) != 0 {
		t.Errorf("len(Tools) = %d, want 0", len(msg.Tools))
	}
	if msg.Tasks == nil {
		t.Error("Tasks must be non-nil (empty snapshot), got nil")
	}
	if len(msg.Tasks) != 0 {
		t.Errorf("len(Tasks) = %d, want 0", len(msg.Tasks))
	}
}

func TestParseLine_BackgroundTasksChanged_UnknownTaskTypePassthrough(t *testing.T) {
	b := New()
	// Hypothetical future task_type — no checked-in fixture carries anything
	// other than local_agent/local_bash today. Exercises the ADR-1 open
	// vocabulary: an unrecognized task_type passes through sanitized under
	// its own tag rather than collapsing into an identity-erasing "other".
	line := `{"type":"system","subtype":"background_tasks_changed","tasks":[` +
		`{"task_id":"tk_1","task_type":"local_web_fetch","description":"fetch"}` +
		`]}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.Tasks) != 1 {
		t.Fatalf("len(Tasks) = %d, want 1", len(msg.Tasks))
	}
	if msg.Tasks[0].Kind != agentrun.BackgroundKind("local_web_fetch") {
		t.Errorf("Tasks[0].Kind = %q, want %q (sanitized passthrough)", msg.Tasks[0].Kind, "local_web_fetch")
	}
	// Not the subagent task_type: excluded from the transitional Tools view.
	if len(msg.Tools) != 0 {
		t.Errorf("len(Tools) = %d, want 0 (unknown kind stays out of Tools)", len(msg.Tools))
	}
}

func TestParseLine_BackgroundTasksChanged_ControlCharTaskType(t *testing.T) {
	b := New()
	// A control character in task_type must be rejected by SanitizeCode,
	// yielding an empty Kind rather than a raw pass-through of unsafe bytes.
	// The escape sequence is assembled from rune 92 (backslash) at runtime
	// rather than typed literally, so this source file never carries a raw
	// control byte.
	nulEscape := string(rune(92)) + "u0000"
	line := `{"type":"system","subtype":"background_tasks_changed","tasks":[` +
		`{"task_id":"tk_1","task_type":"bad` + nulEscape + `type","description":"x"}` +
		`]}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msg.Tasks) != 1 {
		t.Fatalf("len(Tasks) = %d, want 1", len(msg.Tasks))
	}
	if msg.Tasks[0].Kind != "" {
		t.Errorf("Tasks[0].Kind = %q, want empty (control char rejection)", msg.Tasks[0].Kind)
	}
}

// --- task_notification -> MessageTaskResult ---

func TestParseLine_TaskNotificationTerminal_MapsToTaskResult(t *testing.T) {
	b := New()
	line := `{"type":"system","subtype":"task_notification","task_id":"a6b3f55a",` +
		`"tool_use_id":"toolu_spawn","status":"completed","summary":"all done"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageTaskResult {
		t.Fatalf("Type = %q, want %q", msg.Type, agentrun.MessageTaskResult)
	}
	// Correlated by the notification's tool_use_id, NOT parent_tool_use_id
	// (which is absent on notification lines).
	if msg.ParentToolUseID != "toolu_spawn" {
		t.Errorf("ParentToolUseID = %q, want %q", msg.ParentToolUseID, "toolu_spawn")
	}
	if msg.Content != "all done" {
		t.Errorf("Content = %q, want %q", msg.Content, "all done")
	}
	// Tasks correlates by the notification's task_id (the snapshot's id-space,
	// distinct from tool_use_id above) with Kind left empty — a terminal
	// notification carries no task_type, so the kind is genuinely unknowable.
	if len(msg.Tasks) != 1 {
		t.Fatalf("len(Tasks) = %d, want 1", len(msg.Tasks))
	}
	if msg.Tasks[0].ID != "a6b3f55a" {
		t.Errorf("Tasks[0].ID = %q, want %q", msg.Tasks[0].ID, "a6b3f55a")
	}
	if msg.Tasks[0].Kind != "" {
		t.Errorf("Tasks[0].Kind = %q, want empty (unknowable from a notification)", msg.Tasks[0].Kind)
	}
}

func TestParseLine_TaskNotificationStopped_MapsToTaskResult(t *testing.T) {
	b := New()
	// "stopped" is the status Claude reports for a background task cancelled
	// at shutdown — must be treated as terminal, same as "completed".
	line := `{"type":"system","subtype":"task_notification","task_id":"a6b3f55a",` +
		`"tool_use_id":"toolu_spawn","status":"stopped","summary":"cancelled at shutdown"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageTaskResult {
		t.Fatalf("Type = %q, want %q", msg.Type, agentrun.MessageTaskResult)
	}
	if msg.ParentToolUseID != "toolu_spawn" {
		t.Errorf("ParentToolUseID = %q, want %q", msg.ParentToolUseID, "toolu_spawn")
	}
	if msg.Content != "cancelled at shutdown" {
		t.Errorf("Content = %q, want %q", msg.Content, "cancelled at shutdown")
	}
}

func TestParseLine_TaskNotificationNonTerminal_StaysSystem(t *testing.T) {
	b := New()
	// A progress-style notification must not be treated as a completion.
	line := `{"type":"system","subtype":"task_notification","task_id":"a6b3f55a",` +
		`"tool_use_id":"toolu_spawn","status":"running"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageSystem {
		t.Errorf("Type = %q, want %q (non-terminal)", msg.Type, agentrun.MessageSystem)
	}
	if msg.ParentToolUseID != "" {
		t.Errorf("ParentToolUseID = %q, want empty for non-terminal", msg.ParentToolUseID)
	}
}
