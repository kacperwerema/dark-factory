package queue

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 31, 9, 0, 0, 0, time.Local)

func labels(q *Queue) string {
	var s []string
	for _, it := range q.Items {
		s = append(s, it.Label())
	}
	return strings.Join(s, ",")
}

func TestAddMoveBlock(t *testing.T) {
	q := &Queue{NextID: 1}
	for _, s := range []string{"a", "b", "c"} {
		if _, err := q.Add(Item{Slug: s}, Where{}, t0); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.Add(Item{Slug: "b"}, Where{}, t0); err == nil || !strings.Contains(err.Error(), "already item 2") {
		t.Fatalf("duplicate add: %v", err)
	}
	if _, err := q.Add(Item{}, Where{}, t0); err == nil {
		t.Fatal("empty item added")
	}
	id, err := q.Add(Item{Idea: "think of x"}, Where{Top: true}, t0)
	if err != nil || id != 4 || labels(q) != "think of x,a,b,c" {
		t.Fatalf("add --top: %d %v %s", id, err, labels(q))
	}
	if err := q.Move("c", Where{Before: "a"}); err != nil || labels(q) != "think of x,c,a,b" {
		t.Fatalf("move before: %v %s", err, labels(q))
	}
	if err := q.Move("#4", Where{After: "b"}); err != nil || labels(q) != "c,a,b,think of x" {
		t.Fatalf("move after: %v %s", err, labels(q))
	}
	if err := q.Move("a", Where{Bottom: true}); err != nil || labels(q) != "c,b,think of x,a" {
		t.Fatalf("move bottom: %v %s", err, labels(q))
	}
	if err := q.Move("a", Where{}); err == nil {
		t.Fatal("move without a place")
	}
	if err := q.Block("a", "3"); err != nil {
		t.Fatal(err)
	}
	if err := q.Block("a", "a"); err == nil {
		t.Fatal("blocked on itself")
	}
	if err := q.Block("a", "99"); err == nil {
		t.Fatal("blocked on a missing item")
	}
	q.Block("a", "other/slug")
	if got := strings.Join(q.Find("a").BlockedOn, ","); got != "#3,other/slug" {
		t.Fatalf("blocked_on = %s", got)
	}
	if err := q.Unblock("a", "3"); err != nil || strings.Join(q.Find("a").BlockedOn, ",") != "other/slug" {
		t.Fatalf("unblock: %v %v", err, q.Find("a").BlockedOn)
	}
	isDone := func(slug string) (bool, bool) { return slug == "other/slug", true }
	if done, found := q.BlockerDone("other/slug", isDone); !done || !found {
		t.Fatal("slug blocker")
	}
	if done, _ := q.BlockerDone("#2", isDone); done {
		t.Fatal("open item counted done")
	}
	if ids := q.MarkDone("B", "abc", t0); len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("mark done: %v", ids)
	}
	if done, _ := q.BlockerDone("#2", isDone); !done {
		t.Fatal("done item not counted done")
	}
	if got := q.DoneOn(t0); len(got) != 1 || got[0].DoneSHA != "abc" {
		t.Fatalf("done today: %+v", got)
	}
	if got := q.DoneOn(t0.Add(48 * time.Hour)); len(got) != 0 {
		t.Fatalf("done two days later: %+v", got)
	}
	// A done slug can be queued again.
	if _, err := q.Add(Item{Slug: "b"}, Where{}, t0); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateRoundTripAndConcurrency(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "issues")
	if Exists(dir) {
		t.Fatal("exists before a write")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := Update(dir, func(q *Queue) error {
				_, err := q.Add(Item{Idea: "idea " + string(rune('a'+i))}, Where{}, t0)
				return err
			})
			if err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	q, err := Load(dir)
	if err != nil || len(q.Items) != 8 || q.NextID != 9 {
		t.Fatalf("after 8 concurrent adds: %v %d items, next %d", err, len(q.Items), q.NextID)
	}
	if err := os.WriteFile(Path(dir), []byte("{bad"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "queue.json") {
		t.Fatalf("corrupt file: %v", err)
	}
}
