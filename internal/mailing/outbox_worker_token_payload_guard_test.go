package mailing

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestTokenGatedKindsPayloadCarriesConfirmToken is #0401's guard.
//
// #0400 made tokenGatedKinds a true set (map[outbox.Kind]struct{}), so a
// member can no longer be present-but-DISARMED — a security failure, mail
// going out that should not. Nothing, before this test, checked the mirror
// case: a member present but UNGATEABLE — an availability failure, mail
// that should go out silently never does.
//
// tokenGate (outbox_worker.go, below render's payload structs) unmarshals
// row.Payload into a struct with exactly one field, `json:"confirm_token"`.
// If a kind is added to tokenGatedKinds whose producer payload never sets
// one, ConfirmToken decodes to "" and tokenGate's own
// unreachable-by-construction branch — "queued row's payload carries no
// confirm token" — fires on EVERY message of that kind. No error, no
// bounce, no backlog: the row terminates 'skipped' and looks handled.
//
// #0400's review found this by mutation: moving outbox.KindAlreadySubscribed
// from tokenUngatedKinds into tokenGatedKinds, partition kept intact (so
// TestTokenGatedKindsPartitionEveryMailKind stays green), leaves the ENTIRE
// internal/mailing suite passing — alreadySubscribedPayload carries only
// ManageToken. Reproduced independently before this guard was written (see
// this issue's ## Verification): `go test ./internal/mailing/...` exits 0
// against that exact mutant in a git-archive export on a private scratch
// database, and a throwaway probe calling tokenGate directly on an
// already_subscribed row returns
// skip=true, reason="queued row's payload carries no confirm token".
//
// # Both sides come from source, not a hand-written list
//
// CLAUDE.md §8: a hand-written list is a copy of the answer stored next to
// the question, and this whole issue family (#0400, and #0340 before it)
// exists because such a copy agreed with itself. Neither side here is one:
//
//   - The GATED SET is the actual, live tokenGatedKinds package var, read
//     directly — not a re-parsed literal of it — so a kind added there is
//     picked up the instant the map changes, with no second copy to drift.
//   - Each gated kind's PAYLOAD TYPE comes from parsing render's own switch
//     statement over row.Kind (renderPayloadTypesByKindIdent, below): for
//     each `case outbox.KindX:` this reads that case's own
//     `var p XxxPayload` declaration. That is the SAME resolution render
//     performs when it actually runs that case — this guard does not
//     invent a second, independently-maintained mapping from kind to
//     payload type, it reads the one render already has.
//   - Each payload type's FIELDS come from parsing this package's struct
//     type declarations and reading their tags with reflect.StructTag
//     (payloadTypeHasJSONField, below) — the same tag-reading mechanism
//     encoding/json itself uses to resolve `confirm_token` when tokenGate
//     unmarshals row.Payload at send time.
//
// So a kind is judged to "carry a confirm_token" exactly when render's own
// switch would decode row.Payload into a struct declaring that field — the
// identical resolution tokenGate performs at runtime, checked here ahead of
// time instead of learned from a silently withheld row.
//
// Mutation proof (recorded in #0401's ## Verification): moving
// outbox.KindAlreadySubscribed into tokenGatedKinds (partition kept intact)
// fails this test, naming outbox.KindAlreadySubscribed; an unmutated
// control passes.
func TestTokenGatedKindsPayloadCarriesConfirmToken(t *testing.T) {
	fset := token.NewFileSet()
	nonTestGo := func(fi fs.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }

	outboxPkgs, err := parser.ParseDir(fset, "../outbox", nonTestGo, 0)
	if err != nil {
		t.Fatalf("parsing internal/outbox source: %v", err)
	}
	kindIdentToValue := outboxKindIdentValues(filesOf(outboxPkgs))
	if len(kindIdentToValue) == 0 {
		t.Fatal("found zero `KindX Kind = \"x\"` constants in internal/outbox — the parse likely broke, not a real empty set")
	}
	kindValueToIdent := make(map[string]string, len(kindIdentToValue))
	for ident, value := range kindIdentToValue {
		kindValueToIdent[value] = ident
	}

	mailingPkgs, err := parser.ParseDir(fset, ".", nonTestGo, 0)
	if err != nil {
		t.Fatalf("parsing internal/mailing source: %v", err)
	}
	mailingFiles := filesOf(mailingPkgs)

	payloadTypeByKindIdent := renderPayloadTypesByKindIdent(t, mailingFiles)
	if len(payloadTypeByKindIdent) == 0 {
		t.Fatal("render's switch over row.Kind yielded zero kind->payload-type mappings — the parse likely broke, not a real empty set")
	}

	// The gated set itself: the LIVE package var, not a re-parsed copy.
	if len(tokenGatedKinds) == 0 {
		t.Fatal("tokenGatedKinds is empty — that would make every branch below vacuous, not a real pass")
	}
	gated := make([]string, 0, len(tokenGatedKinds))
	for k := range tokenGatedKinds {
		gated = append(gated, string(k))
	}
	sort.Strings(gated)

	for _, kindValue := range gated {
		ident, ok := kindValueToIdent[kindValue]
		if !ok {
			t.Errorf("tokenGatedKinds member %q could not be resolved to a declared outbox.Kind identifier (unexpected constant declaration style in internal/outbox) — cannot verify its producer payload carries confirm_token", kindValue)
			continue
		}
		payloadType, ok := payloadTypeByKindIdent[ident]
		if !ok {
			t.Errorf("tokenGatedKinds member outbox.%s (%q) has no corresponding case in render's switch (outbox_worker.go) declaring a payload variable — cannot verify it carries confirm_token", ident, kindValue)
			continue
		}
		has, err := payloadTypeHasJSONField(mailingFiles, payloadType, "confirm_token")
		if err != nil {
			t.Errorf("tokenGatedKinds member outbox.%s (%q), payload type %s: %v", ident, kindValue, payloadType, err)
			continue
		}
		if !has {
			t.Errorf("tokenGatedKinds member outbox.%s (%q)'s producer payload (%s) declares no \"confirm_token\" json field — tokenGate will withhold EVERY message of this kind with reason \"queued row's payload carries no confirm token\", and nothing else in the suite notices (#0401)", ident, kindValue, payloadType)
		}
	}
}

// outboxKindIdentValues parses files (internal/outbox's own, non-test
// source) for every `KindX Kind = "value"` constant declaration and returns
// identifier name -> literal string value. Deliberately narrower than
// collectKindConstants (outbox_worker_kinds_guard_test.go, #0282), which
// resolves five different declaration styles for a different purpose
// (finding every Kind, however spelled): this guard only needs a literal
// string value to reverse-look-up a live tokenGatedKinds entry, and every
// one of internal/outbox's Kind constants today uses this exact single
// style. A constant declared some other style is simply absent from the
// returned map; the caller reports that as an inability to verify rather
// than silently treating it as covered.
func outboxKindIdentValues(files []*ast.File) map[string]string {
	out := map[string]string{}
	for _, file := range files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				id, ok := vs.Type.(*ast.Ident)
				if !ok || id.Name != "Kind" {
					continue
				}
				lit, ok := vs.Values[0].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}
				out[vs.Names[0].Name] = value
			}
		}
	}
	return out
}

// renderPayloadTypesByKindIdent parses files (this package's own source)
// for render's switch over row.Kind and returns, for every case whose body
// declares `var p <Type>`, a map from that case's outbox.Kind identifier(s)
// to the declared type name. A case with no such declaration — render's
// default branch (goodbye: "no producer yet") — contributes nothing, which
// is correct: there is no payload to check yet.
//
// This is the SAME lookup render itself performs when it actually runs
// that case, read from render's own source rather than reimplemented as a
// second, independently-maintained table that could drift from it.
func renderPayloadTypesByKindIdent(t *testing.T, files []*ast.File) map[string]string {
	t.Helper()
	out := map[string]string{}
	var foundRender bool
	for _, file := range files {
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Name.Name != "render" || fd.Recv == nil || len(fd.Recv.List) != 1 {
				continue
			}
			star, ok := fd.Recv.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			recvIdent, ok := star.X.(*ast.Ident)
			if !ok || recvIdent.Name != "OutboxWorker" {
				continue
			}
			foundRender = true

			ast.Inspect(fd.Body, func(n ast.Node) bool {
				sw, ok := n.(*ast.SwitchStmt)
				if !ok {
					return true
				}
				for _, stmt := range sw.Body.List {
					cc, ok := stmt.(*ast.CaseClause)
					if !ok || cc.List == nil {
						continue // nil List is the `default:` clause
					}
					var idents []string
					for _, expr := range cc.List {
						sel, ok := expr.(*ast.SelectorExpr)
						if !ok {
							continue
						}
						pkgIdent, ok := sel.X.(*ast.Ident)
						if !ok || pkgIdent.Name != "outbox" {
							continue
						}
						idents = append(idents, sel.Sel.Name)
					}
					payloadType := payloadVarTypeIn(cc.Body)
					if payloadType == "" {
						continue
					}
					for _, ident := range idents {
						out[ident] = payloadType
					}
				}
				return false // this switch's cases are fully handled above
			})
		}
	}
	if !foundRender {
		t.Fatal("could not find func (w *OutboxWorker) render in internal/mailing source — the parse likely broke")
	}
	return out
}

// payloadVarTypeIn returns the declared type name of a `var p <Type>`
// statement among stmts, or "" if none is present.
func payloadVarTypeIn(stmts []ast.Stmt) string {
	for _, stmt := range stmts {
		ds, ok := stmt.(*ast.DeclStmt)
		if !ok {
			continue
		}
		gd, ok := ds.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "p" {
				continue
			}
			id, ok := vs.Type.(*ast.Ident)
			if !ok {
				continue
			}
			return id.Name
		}
	}
	return ""
}

// payloadTypeHasJSONField parses files (this package's own source) for a
// struct type declaration named typeName and reports whether any field
// carries a `json:"<jsonName>"` tag — the option-free field name; anything
// after a comma (`,omitempty` etc.) is ignored, matching encoding/json's
// own resolution via reflect.StructTag.Get. Returns an error, rather than
// a false, when typeName cannot be found at all: a missing type is a
// different failure than a type that exists but lacks the field, and the
// caller reports the two differently.
func payloadTypeHasJSONField(files []*ast.File, typeName, jsonName string) (bool, error) {
	for _, file := range files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != typeName {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					return false, fmt.Errorf("%s is declared but is not a struct type", typeName)
				}
				for _, field := range st.Fields.List {
					if field.Tag == nil {
						continue
					}
					tagText, err := parseStructTagLiteral(field.Tag.Value)
					if err != nil {
						continue
					}
					name := reflect.StructTag(tagText).Get("json")
					if idx := strings.IndexByte(name, ','); idx >= 0 {
						name = name[:idx]
					}
					if name == jsonName {
						return true, nil
					}
				}
				return false, nil
			}
		}
	}
	return false, fmt.Errorf("no struct type named %s found in internal/mailing source", typeName)
}

// parseStructTagLiteral turns an *ast.BasicLit's raw source text for a
// struct tag into the tag's actual bytes. Every payload struct in this
// package uses Go's ordinary backtick raw-string form for its tags; the
// strconv.Unquote fallback covers the (unused today) double-quoted form, so
// a tag written that way is checked rather than silently skipped.
func parseStructTagLiteral(raw string) (string, error) {
	if len(raw) >= 2 && raw[0] == '`' && raw[len(raw)-1] == '`' {
		return raw[1 : len(raw)-1], nil
	}
	return strconv.Unquote(raw)
}
