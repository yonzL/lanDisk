package perms

import (
	"testing"

	"land/internal/store"
)

func policy(rules ...store.Rule) Policy { return Compile(rules) }

func TestLongestPrefixWins(t *testing.T) {
	p := policy(
		store.Rule{RelPath: "", Perm: store.PermRead},
		store.Rule{RelPath: "公开", Perm: store.PermWrite},
		store.Rule{RelPath: "公开/只读", Perm: store.PermRead},
		store.Rule{RelPath: "财务", Perm: store.PermNone},
	)

	cases := []struct {
		path string
		want store.Perm
	}{
		{"", store.PermRead},
		{"图纸", store.PermRead},
		{"公开", store.PermWrite},
		{"公开/子目录", store.PermWrite},
		{"公开/只读", store.PermRead},
		{"公开/只读/更深", store.PermRead},
		{"财务", store.PermNone},
		{"财务/2024", store.PermNone},
	}
	for _, tc := range cases {
		if got := p.Decide(tc.path, Visitor{}); got != tc.want {
			t.Fatalf("%q → %s, want %s", tc.path, got, tc.want)
		}
	}
}

func TestSiblingPrefixDoesNotLeak(t *testing.T) {
	p := policy(
		store.Rule{RelPath: "", Perm: store.PermNone},
		store.Rule{RelPath: "a/b", Perm: store.PermRead},
	)
	if p.CanRead("a/bc", Visitor{}) {
		t.Fatal("rule a/b leaked access to a/bc")
	}
}

func TestMissingRootRuleHidesShare(t *testing.T) {
	p := policy(store.Rule{RelPath: "公开", Perm: store.PermWrite})
	if p.CanRead("", Visitor{}) {
		t.Fatal("share without a root rule is visible")
	}
}

func TestAdminBypassesRules(t *testing.T) {
	p := policy(store.Rule{RelPath: "", Perm: store.PermNone})
	admin := Visitor{Admin: true}
	if !p.CanRead("财务", admin) || !p.CanWrite("财务", admin) {
		t.Fatal("admin was not granted full access")
	}
}

func TestCanWriteRequiresRW(t *testing.T) {
	p := policy(
		store.Rule{RelPath: "", Perm: store.PermRead},
		store.Rule{RelPath: "公共", Perm: store.PermWrite},
	)
	if p.CanWrite("", Visitor{}) {
		t.Fatal("read-only root granted write")
	}
	if !p.CanWrite("公共/上传", Visitor{}) {
		t.Fatal("rw subtree denied write")
	}
}

func TestNormalizeRejectsTraversalAndReservedNames(t *testing.T) {
	valid := map[string]string{
		"":         "",
		"/":        "",
		"a/b":      "a/b",
		" a\\b\\ ": "a/b",
		"/a//b/":   "a/b",
		"a/./b":    "a/b",
	}
	for in, want := range valid {
		got, ok := Normalize(in)
		if !ok || got != want {
			t.Fatalf("Normalize(%q) = (%q,%v), want (%q,true)", in, got, ok, want)
		}
	}

	invalid := []string{"..", "../etc", "a/../../b", ".land-trash", "x/.landpart-123", "a/.landrestore-9"}
	for _, in := range invalid {
		if _, ok := Normalize(in); ok {
			t.Fatalf("Normalize(%q) accepted a reserved or escaping path", in)
		}
	}
}

func TestRuleMatchingIsCaseInsensitive(t *testing.T) {
	p := policy(
		store.Rule{RelPath: "", Perm: store.PermWrite},
		store.Rule{RelPath: "HR", Perm: store.PermNone},
	)
	for _, path := range []string{"HR", "hr", "Hr/合同.pdf", "hr/sub/deep"} {
		if p.CanRead(path, Visitor{}) {
			t.Fatalf("%q bypassed the HR=none rule", path)
		}
	}
	if !p.CanRead("HRX", Visitor{}) {
		t.Fatal("case folding loosened the segment boundary")
	}
}

func TestNormalizeRejectsDottedReservedNames(t *testing.T) {
	for _, in := range []string{".land-trash.", ".land-trash./x", "a/.landpart-1.", ".landrestore-2 ."} {
		if _, ok := Normalize(in); ok {
			t.Fatalf("Normalize(%q) accepted an NTFS-resolvable reserved name", in)
		}
	}
}
