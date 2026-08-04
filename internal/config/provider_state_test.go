package config

import (
	"testing"

	"github.com/xingshizhai/issue-flow/internal/domain"
)

func TestProviderStateForPrecedence(t *testing.T) {
	t.Parallel()
	w := Workflow{}
	if got := w.ProviderStateFor(domain.StateDone); got != "" {
		t.Fatalf("default done = %q", got)
	}
	w.AutoClose = true
	if got := w.ProviderStateFor(domain.StateDone); got != "closed" {
		t.Fatalf("auto_close done = %q", got)
	}
	if got := w.ProviderStateFor(domain.StateWorking); got != "" {
		t.Fatalf("auto_close working = %q", got)
	}
	w.SyncProviderState = true
	if got := w.ProviderStateFor(domain.StateWorking); got != "progressing" {
		t.Fatalf("sync working = %q", got)
	}
	if got := w.ProviderStateFor(domain.StateReview); got != "progressing" {
		t.Fatalf("sync review = %q", got)
	}
	w.ProviderStates = map[string]string{"review": "open", "done": "rejected"}
	if got := w.ProviderStateFor(domain.StateReview); got != "open" {
		t.Fatalf("override review = %q", got)
	}
	if got := w.ProviderStateFor(domain.StateDone); got != "rejected" {
		t.Fatalf("override done = %q", got)
	}
	if got := w.ProviderStateFor(domain.StateClaimed); got != "progressing" {
		t.Fatalf("fallback claimed = %q", got)
	}
}

func TestEnterpriseStateFor(t *testing.T) {
	t.Parallel()
	w := Workflow{}
	if got := w.EnterpriseStateFor(domain.StateReview); got != "" {
		t.Fatalf("empty map = %q", got)
	}
	w.EnterpriseStates = map[string]string{"review": "已修复", "working": "修复中"}
	if got := w.EnterpriseStateFor(domain.StateReview); got != "已修复" {
		t.Fatalf("review = %q", got)
	}
	if got := w.EnterpriseStateFor(domain.StateDone); got != "" {
		t.Fatalf("done = %q", got)
	}
}
