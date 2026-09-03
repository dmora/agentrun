package acp

import (
	"strings"
	"testing"

	"github.com/dmora/agentrun"
)

func TestSessionConfigCalls_CountOnly(t *testing.T) {
	tests := []struct {
		name          string
		session       agentrun.Session
		configOptions []sessionConfigOption
		wantCount     int
	}{
		{
			name:      "empty session returns no calls",
			session:   agentrun.Session{},
			wantCount: 0,
		},
		{
			name:      "model without matching config option produces no call",
			session:   agentrun.Session{Model: "gpt-4"},
			wantCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := sessionConfigCalls("test-session", tt.session, nil, tt.configOptions)
			if len(calls) != tt.wantCount {
				t.Fatalf("got %d calls, want %d", len(calls), tt.wantCount)
			}
		})
	}
}

func TestSessionConfigCalls_ModeSkippedWithoutModes(t *testing.T) {
	session := agentrun.Session{
		Options: map[string]string{agentrun.OptionMode: "plan"},
	}
	// Agent didn't advertise modes — set_mode should be skipped.
	calls := sessionConfigCalls("test-session", session, nil, nil)
	if len(calls) != 0 {
		t.Fatalf("got %d calls, want 0 (agent has no modes)", len(calls))
	}
}

func TestSessionConfigCalls_ModeSetting(t *testing.T) {
	session := agentrun.Session{
		Options: map[string]string{agentrun.OptionMode: "plan"},
	}
	modes := &sessionModeState{
		AvailableModes: []sessionMode{{ID: "plan"}, {ID: "act"}},
	}
	calls := sessionConfigCalls("test-session", session, modes, nil)
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	if calls[0].Method != MethodSessionSetMode {
		t.Errorf("method = %q, want %q", calls[0].Method, MethodSessionSetMode)
	}
	p := calls[0].Params.(setModeParams)
	if p.ModeID != "plan" {
		t.Errorf("modeId = %q, want %q", p.ModeID, "plan")
	}
	if p.SessionID != "test-session" {
		t.Errorf("sessionId = %q, want %q", p.SessionID, "test-session")
	}
}

func TestSessionConfigCalls_ModelSetting(t *testing.T) {
	session := agentrun.Session{Model: "gpt-4"}
	opts := []sessionConfigOption{
		{ID: "model", Name: "Model", Category: "model"},
	}
	calls := sessionConfigCalls("test-session", session, nil, opts)
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	if calls[0].Method != MethodSessionSetConfig {
		t.Errorf("method = %q, want %q", calls[0].Method, MethodSessionSetConfig)
	}
	p := calls[0].Params.(setConfigOptionParams)
	if p.ConfigID != "model" {
		t.Errorf("configId = %q, want %q", p.ConfigID, "model")
	}
	if p.Value != "gpt-4" {
		t.Errorf("value = %q, want %q", p.Value, "gpt-4")
	}
}

func TestSessionConfigCalls_OpaqueAdvertisedOption(t *testing.T) {
	session := agentrun.Session{Options: map[string]string{
		SessionConfigOption("quality"): "thorough",
	}}
	opts := []sessionConfigOption{
		{ID: "quality", Name: "Quality", Options: []configOptionChoice{{Value: "fast"}, {Value: "thorough"}}},
	}
	calls := sessionConfigCalls("test-session", session, nil, opts)
	if len(calls) != 1 {
		t.Fatalf("got %d calls, want 1", len(calls))
	}
	p := calls[0].Params.(setConfigOptionParams)
	if p.ConfigID != "quality" || p.Value != "thorough" {
		t.Fatalf("params = %#v, want quality=thorough", p)
	}
}

func TestSessionConfigCalls_UnadvertisedOpaqueOptionSkipped(t *testing.T) {
	session := agentrun.Session{Options: map[string]string{
		SessionConfigOption("missing"): "value",
	}}
	if calls := sessionConfigCalls("test-session", session, nil, nil); len(calls) != 0 {
		t.Fatalf("got %d calls, want 0", len(calls))
	}
}

func TestPublicConfigOptionModels(t *testing.T) {
	models := publicConfigOptionModels(sessionConfigOption{Options: []configOptionChoice{
		{Value: "base-model", Name: "Base Model"},
		{Value: "", Name: "Invalid"},
	}})
	if len(models) != 1 || models[0].ID != "base-model" || models[0].Name != "Base Model" {
		t.Fatalf("models = %#v", models)
	}
}

func TestConfigOptionCategory(t *testing.T) {
	opts := []sessionConfigOption{
		{ID: "model-id", Category: "model"},
		{ID: "quality-id", Category: "_quality"},
	}
	if got := configOptionCategory(opts, "model-id"); got != "model" {
		t.Fatalf("model category = %q", got)
	}
	if got := configOptionCategory(opts, "quality-id"); got != "_quality" {
		t.Fatalf("quality category = %q", got)
	}
	if got := configOptionCategory(opts, "missing"); got != "" {
		t.Fatalf("missing category = %q", got)
	}
}

func TestValidateSessionModelUsesAdvertisedModelConfig(t *testing.T) {
	models := &sessionModelState{
		CurrentModelID: "base-model[medium]",
		AvailableModels: []modelInfo{
			{ID: "base-model[low]"},
			{ID: "base-model[medium]"},
		},
	}
	configOptions := []sessionConfigOption{{
		ID: "model", Category: "model",
		Options: []configOptionChoice{{Value: "base-model", Name: "Base Model"}},
	}}
	if err := validateSessionModel("base-model", models, configOptions); err != nil {
		t.Fatalf("validate base model: %v", err)
	}
	if err := validateSessionModel("missing-model", models, configOptions); err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("validate missing model error = %v", err)
	}
}

func TestSessionConfigCalls_ModeAndModelOrder(t *testing.T) {
	session := agentrun.Session{
		Model:   "claude-4",
		Options: map[string]string{agentrun.OptionMode: "act"},
	}
	modes := &sessionModeState{
		AvailableModes: []sessionMode{{ID: "act"}, {ID: "plan"}},
	}
	opts := []sessionConfigOption{
		{ID: "model", Name: "Model", Category: "model"},
	}
	calls := sessionConfigCalls("test-session", session, modes, opts)
	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
	if calls[0].Method != MethodSessionSetMode {
		t.Errorf("first call method = %q, want %q", calls[0].Method, MethodSessionSetMode)
	}
	if calls[1].Method != MethodSessionSetConfig {
		t.Errorf("second call method = %q, want %q", calls[1].Method, MethodSessionSetConfig)
	}
}
