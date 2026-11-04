package store

import (
	"context"
	"errors"
	"testing"
)

func TestCreateShareWritesRootRule(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sh, err := st.CreateShare(ctx, Share{Name: "图纸", VirtualPath: "/图纸", Root: `D:\drawing`}, PermRead)
	if err != nil {
		t.Fatalf("create share: %v", err)
	}
	if sh.ID == 0 {
		t.Fatal("share id not assigned")
	}

	rules, err := st.Rules(ctx, sh.ID)
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if len(rules) != 1 || rules[0].RelPath != "" || rules[0].Perm != PermRead {
		t.Fatalf("root rule = %+v, want ('', r)", rules)
	}
}

func TestCreateShareRejectsDuplicateVirtualPath(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	if _, err := st.CreateShare(ctx, Share{Name: "a", VirtualPath: "/a", Root: "/tmp/a"}, PermRead); err != nil {
		t.Fatalf("first share: %v", err)
	}
	_, err := st.CreateShare(ctx, Share{Name: "b", VirtualPath: "/a", Root: "/tmp/b"}, PermRead)
	if err == nil {
		t.Fatal("duplicate virtual path accepted")
	}
}

func TestUpdateShareDoesNotChangeRoot(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sh, err := st.CreateShare(ctx, Share{Name: "a", VirtualPath: "/a", Root: "/tmp/a"}, PermRead)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	sh.Name = "renamed"
	sh.Root = "/tmp/elsewhere"
	if err := st.UpdateShare(ctx, sh); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := st.ShareByID(ctx, sh.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Name != "renamed" {
		t.Fatalf("name = %q, want renamed", got.Name)
	}
	if got.Root != "/tmp/a" {
		t.Fatalf("root = %q, want the original (root is immutable)", got.Root)
	}
}

func TestDeleteShareRemovesRules(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sh, err := st.CreateShare(ctx, Share{Name: "a", VirtualPath: "/a", Root: "/tmp/a"}, PermRead)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.DeleteShare(ctx, sh.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.ShareByID(ctx, sh.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("share still readable: %v", err)
	}
	rules, err := st.Rules(ctx, sh.ID)
	if err != nil {
		t.Fatalf("rules: %v", err)
	}
	if len(rules) != 0 {
		t.Fatalf("rules survived share deletion: %+v", rules)
	}
}

func TestSetRuleUpsertsAndRootCannotBeDeleted(t *testing.T) {
	st := openTemp(t)
	ctx := context.Background()

	sh, err := st.CreateShare(ctx, Share{Name: "a", VirtualPath: "/a", Root: "/tmp/a"}, PermRead)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := st.SetRule(ctx, Rule{ShareID: sh.ID, RelPath: "公开", Perm: PermRead}); err != nil {
		t.Fatalf("set rule: %v", err)
	}
	if err := st.SetRule(ctx, Rule{ShareID: sh.ID, RelPath: "公开", Perm: PermWrite}); err != nil {
		t.Fatalf("upsert rule: %v", err)
	}
	rules, _ := st.Rules(ctx, sh.ID)
	if len(rules) != 2 || rules[1].Perm != PermWrite {
		t.Fatalf("rules after upsert = %+v", rules)
	}

	if err := st.DeleteRule(ctx, sh.ID, ""); err == nil {
		t.Fatal("root rule was deleted")
	}
}
