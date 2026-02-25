package acp

import "testing"

func TestSelectPermissionOption_NoMatchingKinds(t *testing.T) {
	options := []permissionOpt{
		{OptionID: "opt-1", Name: "Custom", Kind: "custom_action"},
		{OptionID: "opt-2", Name: "Other", Kind: "other_action"},
	}
	result := selectPermissionOption(options, "allow_once", "allow_always")
	if result.Outcome.Outcome != "cancelled" {
		t.Errorf("outcome = %q, want %q", result.Outcome.Outcome, "cancelled")
	}
	if result.Outcome.OptionID != "" {
		t.Errorf("optionID = %q, want empty", result.Outcome.OptionID)
	}
}

func TestSelectPermissionOption_MatchesFirst(t *testing.T) {
	options := []permissionOpt{
		{OptionID: "opt-reject", Name: "Reject", Kind: "reject_once"},
		{OptionID: "opt-allow", Name: "Allow", Kind: "allow_once"},
		{OptionID: "opt-always", Name: "Always", Kind: "allow_always"},
	}
	result := selectPermissionOption(options, "allow_once", "allow_always")
	if result.Outcome.Outcome != "selected" {
		t.Errorf("outcome = %q, want %q", result.Outcome.Outcome, "selected")
	}
	if result.Outcome.OptionID != "opt-allow" {
		t.Errorf("optionID = %q, want %q", result.Outcome.OptionID, "opt-allow")
	}
}

func TestSelectPermissionOption_EmptyOptions(t *testing.T) {
	result := selectPermissionOption(nil, "allow_once")
	if result.Outcome.Outcome != "cancelled" {
		t.Errorf("outcome = %q, want %q", result.Outcome.Outcome, "cancelled")
	}
}

func TestFirstOptionByKind(t *testing.T) {
	options := []permissionOpt{
		{OptionID: "a", Kind: "reject_once"},
		{OptionID: "b", Kind: "allow_once"},
	}
	if got := firstOptionByKind(options, "allow_once"); got != "b" {
		t.Errorf("firstOptionByKind = %q, want %q", got, "b")
	}
	if got := firstOptionByKind(options, "nonexistent"); got != "" {
		t.Errorf("firstOptionByKind = %q, want empty", got)
	}
}
