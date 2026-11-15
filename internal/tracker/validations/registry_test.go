package validations

import (
	"strings"
	"testing"
)

func TestCheck_UnknownRule(t *testing.T) {
	err := Check(Action{Rule: "made_up_rule"}, sampleIssue(), Config{})
	if err == nil || !strings.Contains(err.Error(), "unknown structured rule") {
		t.Fatalf("expected unknown-rule error, got %v", err)
	}
}

func TestCheck_DispatchesToValidator(t *testing.T) {
	err := Check(Action{Rule: "field_in", Field: "priority", Values: []string{"only-this"}}, sampleIssue(), Config{})
	if err == nil || !strings.Contains(err.Error(), "not one of") {
		t.Fatalf("expected dispatch to field_in, got %v", err)
	}
}
