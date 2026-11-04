// Package perms decides "may this visitor read/write this path". It is the only
// place permission rules are interpreted: files, upload and trash all ask it
// instead of re-deriving rules from the database.
package perms

import (
	"strings"

	"land/internal/store"
)

// Visitor is who is asking.
type Visitor struct {
	Admin bool
}

// Policy is one share's rules, compiled for repeated lookups.
type Policy struct {
	rules []rule
}

type rule struct {
	rel  string // normalized + case-folded; "" = share root
	perm store.Perm
}

// Compile builds a policy from a share's rules. Rules with a malformed path or
// permission are skipped rather than trusted. Paths are case-folded because
// NTFS is case-insensitive: a rule typed for "HR" must also cover "hr".
func Compile(rules []store.Rule) Policy {
	compiled := make([]rule, 0, len(rules))
	for _, r := range rules {
		rel, ok := Normalize(r.RelPath)
		if !ok || !store.ValidPerm(r.Perm) {
			continue
		}
		compiled = append(compiled, rule{rel: strings.ToLower(rel), perm: r.Perm})
	}
	return Policy{rules: compiled}
}

// Decide returns the effective permission for relPath. The deepest rule whose
// directory is an ancestor of relPath wins (chfs-style override); without any
// match the answer is "none", so an unconfigured share stays hidden.
func (p Policy) Decide(relPath string, v Visitor) store.Perm {
	if v.Admin {
		return store.PermWrite
	}
	rel, ok := Normalize(relPath)
	if !ok {
		return store.PermNone
	}
	rel = strings.ToLower(rel)

	best := -1
	var perm store.Perm = store.PermNone
	for _, r := range p.rules {
		if !isAncestor(r.rel, rel) {
			continue
		}
		if len(r.rel) > best {
			best = len(r.rel)
			perm = r.perm
		}
	}
	return perm
}

// CanRead reports whether the visitor may list/read the path.
func (p Policy) CanRead(relPath string, v Visitor) bool {
	return p.Decide(relPath, v) != store.PermNone
}

// CanWrite reports whether the visitor may upload/create inside the directory.
func (p Policy) CanWrite(relPath string, v Visitor) bool {
	return p.Decide(relPath, v) == store.PermWrite
}

// isAncestor reports whether dir ("" or "a/b") covers path ("a/b/c"). Both
// arguments are already case-folded.
func isAncestor(dir, path string) bool {
	if dir == "" {
		return true
	}
	if dir == path {
		return true
	}
	return strings.HasPrefix(path, dir+"/")
}

// Normalize converts a client-supplied relative path into the canonical form
// used by rules and notes: forward slashes, no leading/trailing slash, no "."
// or ".." segments, no trailing dots/spaces (NTFS resolves ".land-trash." to
// ".land-trash"), and no reserved names. Empty means the share root. The second
// result is false for paths that must never reach the filesystem.
func Normalize(p string) (string, bool) {
	p = strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	p = strings.Trim(p, "/")
	if p == "" {
		return "", true
	}
	segments := strings.Split(p, "/")
	out := make([]string, 0, len(segments))
	for _, seg := range segments {
		switch seg {
		case "", ".":
			continue
		case "..":
			return "", false
		}
		if strings.ContainsRune(seg, '\x00') {
			return "", false
		}
		// Windows ignores trailing dots and spaces in path components.
		seg = strings.TrimRight(seg, ". ")
		if seg == "" {
			return "", false
		}
		lower := strings.ToLower(seg)
		if lower == ".land-trash" ||
			strings.Contains(lower, ".landpart-") ||
			strings.Contains(lower, ".landrestore-") {
			return "", false
		}
		out = append(out, seg)
	}
	if len(out) == 0 {
		return "", false
	}
	return strings.Join(out, "/"), true
}
