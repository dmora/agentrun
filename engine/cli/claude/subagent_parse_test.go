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

func TestParseLine_BackgroundTasksChanged_FiltersToSubagents(t *testing.T) {
	b := New()
	// A snapshot with one subagent (local_agent) and one background Bash
	// (local_bash). Only the subagent belongs in the pending set.
	line := `{"type":"system","subtype":"background_tasks_changed","tasks":[` +
		`{"task_id":"a6b3f55a","task_type":"local_agent","description":"probe"},` +
		`{"task_id":"bguznt5m","task_type":"local_bash","description":"sleep"}` +
		`]}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageBackgroundTasks {
		t.Fatalf("Type = %q, want %q", msg.Type, agentrun.MessageBackgroundTasks)
	}
	if len(msg.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1 (local_bash filtered out)", len(msg.Tools))
	}
	if msg.Tools[0].ID != "a6b3f55a" {
		t.Errorf("Tools[0].ID = %q, want %q", msg.Tools[0].ID, "a6b3f55a")
	}
}

func TestParseLine_BackgroundTasksChanged_EmptyIsNonNil(t *testing.T) {
	b := New()
	// Drained set: Tools must be non-nil-but-empty so the tracker treats it as
	// an authoritative "zero subagents" snapshot, not "no snapshot".
	line := `{"type":"system","subtype":"background_tasks_changed","tasks":[]}`
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
}

// --- task_notification -> MessageSubagentResult ---

func TestParseLine_TaskNotificationTerminal_MapsToSubagentResult(t *testing.T) {
	b := New()
	line := `{"type":"system","subtype":"task_notification","task_id":"a6b3f55a",` +
		`"tool_use_id":"toolu_spawn","status":"completed","summary":"all done"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageSubagentResult {
		t.Fatalf("Type = %q, want %q", msg.Type, agentrun.MessageSubagentResult)
	}
	// Correlated by the notification's tool_use_id, NOT parent_tool_use_id
	// (which is absent on notification lines).
	if msg.ParentToolUseID != "toolu_spawn" {
		t.Errorf("ParentToolUseID = %q, want %q", msg.ParentToolUseID, "toolu_spawn")
	}
	if msg.Content != "all done" {
		t.Errorf("Content = %q, want %q", msg.Content, "all done")
	}
}

func TestParseLine_TaskNotificationStopped_MapsToSubagentResult(t *testing.T) {
	b := New()
	// "stopped" is the status Claude reports for a background task cancelled
	// at shutdown — must be treated as terminal, same as "completed".
	line := `{"type":"system","subtype":"task_notification","task_id":"a6b3f55a",` +
		`"tool_use_id":"toolu_spawn","status":"stopped","summary":"cancelled at shutdown"}`
	msg, err := b.ParseLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg.Type != agentrun.MessageSubagentResult {
		t.Fatalf("Type = %q, want %q", msg.Type, agentrun.MessageSubagentResult)
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
