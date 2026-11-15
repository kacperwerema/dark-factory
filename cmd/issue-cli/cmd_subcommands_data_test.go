package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kacperwerema/dark-factory/internal/tracker"
)

// ------------------- data -------------------

func TestRunDataAddCreatesSidecar(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runDataAdd(ctx, []string{slug, "--description", "first finding", "--status", "open"}); err != nil {
		t.Fatalf("runDataAdd: %v", err)
	}
	if strings.TrimSpace(stdout.String()) != "1" {
		t.Fatalf("stdout = %q, want \"1\"", stdout.String())
	}

	store, err := tracker.LoadData(issuePath)
	if err != nil {
		t.Fatalf("LoadData: %v", err)
	}
	if len(store.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(store.Entries))
	}
	e := store.Entries[0]
	if e.ID != 1 || e.Description != "first finding" || e.Status != "open" {
		t.Fatalf("entry = %+v", e)
	}

	sidecar := tracker.SidecarPath(issuePath)
	if _, err := os.Stat(sidecar); err != nil {
		t.Fatalf("sidecar missing: %v", err)
	}
	raw, _ := os.ReadFile(sidecar)
	var onDisk tracker.DataStore
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("unmarshal sidecar: %v\n%s", err, raw)
	}
	if onDisk.NextID != 2 {
		t.Fatalf("next_id = %d, want 2", onDisk.NextID)
	}
}

func TestRunDataAddRequiresDescription(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runDataAdd(ctx, []string{slug}); err == nil || !strings.Contains(err.Error(), "--description") {
		t.Fatalf("expected description-required error, got %v", err)
	}
}

func TestRunDataListJSON(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runDataAdd(ctx, []string{slug, "--description", "f1"}); err != nil {
		t.Fatalf("setup: %v", err)
	}

	jctx, jstdout, _ := newTestContext(proj, true)
	if err := runDataList(jctx, []string{slug}); err != nil {
		t.Fatalf("runDataList: %v", err)
	}
	var got []tracker.DataEntry
	if err := json.Unmarshal([]byte(jstdout.String()), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, jstdout.String())
	}
	if len(got) != 1 || got[0].Description != "f1" {
		t.Fatalf("entries = %+v", got)
	}
}

func TestRunDataListEmpty(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runDataList(ctx, []string{slug}); err != nil {
		t.Fatalf("runDataList: %v", err)
	}
	assertContains(t, stdout.String(), "(no entries)")
}

func TestRunDataSetStatusUpdatesEntry(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runDataAdd(ctx, []string{slug, "--description", "f1", "--status", "open"}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := runDataSetStatus(ctx, []string{slug, "1", "resolved"}); err != nil {
		t.Fatalf("runDataSetStatus: %v", err)
	}
	store, _ := tracker.LoadData(issuePath)
	if store.Entries[0].Status != "resolved" {
		t.Fatalf("status = %q, want resolved", store.Entries[0].Status)
	}
}

func TestRunDataSetStatusRequiresAllArgs(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runDataSetStatus(ctx, []string{slug, "1"}); err == nil {
		t.Fatal("expected requires-args error")
	}
}

func TestRunDataSetStatusRejectsInvalidID(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runDataSetStatus(ctx, []string{slug, "abc", "open"}); err == nil || !strings.Contains(err.Error(), "invalid id") {
		t.Fatalf("expected invalid-id error, got %v", err)
	}
}

func TestRunDataSetCommentUpdatesComment(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runDataAdd(ctx, []string{slug, "--description", "f1"}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := runDataSetComment(ctx, []string{slug, "1", "--text", "note"}); err != nil {
		t.Fatalf("runDataSetComment: %v", err)
	}
	store, _ := tracker.LoadData(issuePath)
	if store.Entries[0].Comment != "note" {
		t.Fatalf("comment = %q, want note", store.Entries[0].Comment)
	}
}

func TestRunDataSetCommentRequiresArgs(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runDataSetComment(ctx, []string{slug}); err == nil {
		t.Fatal("expected requires-args error")
	}
}

func TestRunDataRemoveDeletesEntry(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runDataAdd(ctx, []string{slug, "--description", "f1"}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := runDataRemove(ctx, []string{slug, "1"}); err != nil {
		t.Fatalf("runDataRemove: %v", err)
	}
	store, _ := tracker.LoadData(issuePath)
	if len(store.Entries) != 0 {
		t.Fatalf("entries = %d, want 0", len(store.Entries))
	}
}

func TestRunDataRemoveRequiresArgs(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runDataRemove(ctx, nil); err == nil {
		t.Fatal("expected requires-args error")
	}
}

func TestRunDataDispatcherRequiresSubcommand(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runData(ctx, nil); err == nil || !strings.Contains(err.Error(), "requires a subcommand") {
		t.Fatalf("expected requires-subcommand error, got %v", err)
	}
}

func TestRunDataDispatcherRejectsUnknownSubcommand(t *testing.T) {
	proj, _, _ := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runData(ctx, []string{"frobnicate"}); err == nil || !strings.Contains(err.Error(), "unknown data subcommand") {
		t.Fatalf("expected unknown-subcommand error, got %v", err)
	}
}

// makeProjectWithTiersMarker builds a project whose issue body declares a
// tiers= marker, so validateTier and tier-aware list output have something
// to validate against.
func makeProjectWithTiersMarker(t *testing.T) (proj *tracker.Project, slug, issuePath string) {
	t.Helper()
	dir := t.TempDir()
	issuesDir := filepath.Join(dir, "issues")
	if err := os.MkdirAll(issuesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	issuePath = filepath.Join(issuesDir, "tiered.md")
	body := strings.TrimSpace(`
---
title: "tiered"
status: "in progress"
---

<!-- data statuses=open,resolved tiers=🔴 critical,🟢 nice -->
`)
	if err := os.WriteFile(issuePath, []byte(body), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	p := &tracker.Project{Name: "Test", Slug: "test-project", IssueDir: issuesDir}
	return p, "tiered", issuePath
}

func TestRunDataAddPersistsTier(t *testing.T) {
	proj, slug, issuePath := makeProjectWithTiersMarker(t)
	ctx, _, _ := newTestContext(proj, false)
	if err := runDataAdd(ctx, []string{slug, "--description", "f1", "--tier", "🔴 critical"}); err != nil {
		t.Fatalf("runDataAdd: %v", err)
	}
	store, _ := tracker.LoadData(issuePath)
	if store.Entries[0].Tier != "🔴 critical" {
		t.Fatalf("tier = %q, want \"🔴 critical\"", store.Entries[0].Tier)
	}
}

func TestRunDataAddRejectsTierNotInMarker(t *testing.T) {
	proj, slug, _ := makeProjectWithTiersMarker(t)
	ctx, _, _ := newTestContext(proj, false)
	err := runDataAdd(ctx, []string{slug, "--description", "f1", "--tier", "💥 unknown"})
	if err == nil || !strings.Contains(err.Error(), "not in the workflow's declared tiers") {
		t.Fatalf("expected tier-not-declared error, got %v", err)
	}
}

func TestRunDataAddAcceptsAnyTierWhenMarkerHasNone(t *testing.T) {
	proj, slug, issuePath := makeSimpleProject(t, "in progress")
	ctx, _, _ := newTestContext(proj, false)
	if err := runDataAdd(ctx, []string{slug, "--description", "f1", "--tier", "ad-hoc"}); err != nil {
		t.Fatalf("runDataAdd: %v", err)
	}
	store, _ := tracker.LoadData(issuePath)
	if store.Entries[0].Tier != "ad-hoc" {
		t.Fatalf("tier = %q, want \"ad-hoc\"", store.Entries[0].Tier)
	}
}

func TestRunDataSetTierUpdatesAndClears(t *testing.T) {
	proj, slug, issuePath := makeProjectWithTiersMarker(t)
	ctx, _, _ := newTestContext(proj, false)
	if err := runDataAdd(ctx, []string{slug, "--description", "f1"}); err != nil {
		t.Fatalf("setup add: %v", err)
	}
	if err := runDataSetTier(ctx, []string{slug, "1", "🟢 nice"}); err != nil {
		t.Fatalf("set-tier: %v", err)
	}
	store, _ := tracker.LoadData(issuePath)
	if store.Entries[0].Tier != "🟢 nice" {
		t.Fatalf("tier after set = %q", store.Entries[0].Tier)
	}
	if err := runDataSetTier(ctx, []string{slug, "1", ""}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	store, _ = tracker.LoadData(issuePath)
	if store.Entries[0].Tier != "" {
		t.Fatalf("tier after clear = %q, want empty", store.Entries[0].Tier)
	}
}

func TestRunDataSetTierRejectsUndeclared(t *testing.T) {
	proj, slug, _ := makeProjectWithTiersMarker(t)
	ctx, _, _ := newTestContext(proj, false)
	if err := runDataAdd(ctx, []string{slug, "--description", "f1"}); err != nil {
		t.Fatalf("setup add: %v", err)
	}
	err := runDataSetTier(ctx, []string{slug, "1", "💥 unknown"})
	if err == nil || !strings.Contains(err.Error(), "not in the workflow's declared tiers") {
		t.Fatalf("expected tier-not-declared error, got %v", err)
	}
}

func TestRunDataSetTierRequiresArgs(t *testing.T) {
	proj, slug, _ := makeProjectWithTiersMarker(t)
	ctx, _, _ := newTestContext(proj, false)
	if err := runDataSetTier(ctx, []string{slug, "1"}); err == nil {
		t.Fatal("expected requires-args error")
	}
}

func TestRunDataListShowsTierColumnWhenPresent(t *testing.T) {
	proj, slug, _ := makeProjectWithTiersMarker(t)
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runDataAdd(ctx, []string{slug, "--description", "f1", "--tier", "🔴 critical"}); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := runDataList(ctx, []string{slug}); err != nil {
		t.Fatalf("list: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "<🔴 critical>") {
		t.Fatalf("expected tier in list output, got:\n%s", out)
	}
}

func TestRunDataDispatcherRoutesToList(t *testing.T) {
	proj, slug, _ := makeSimpleProject(t, "in progress")
	ctx, stdout, _ := newTestContext(proj, false)
	if err := runData(ctx, []string{"list", slug}); err != nil {
		t.Fatalf("runData list: %v", err)
	}
	assertContains(t, stdout.String(), "(no entries)")
}
