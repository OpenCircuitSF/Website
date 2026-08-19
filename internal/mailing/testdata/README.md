# Golden fixtures — why the `.txt` files are CRLF

These fixtures back the golden tests in `../templates_test.go`
(`TestBuild*Email_Golden`). Each test builds a `Message` and compares its
`HTMLBody` and `TextBody` against the byte content of the matching file here.

The `*.txt` files are genuinely CRLF-sensitive, not accidentally so:
`emailContent.renderText` in `../templates.go` joins lines with literal
`"\r\n"`, matching the line ending mail bodies use per RFC 5322 and matching
the rest of this project's plain-text sends (`internal/auth`). The `*.html`
files are not CRLF-sensitive — `renderHTML` joins with plain `"\n"` — so only
the `.txt` fixtures carry the override below.

`.gitattributes` marks `internal/mailing/testdata/*.txt -text` so git does
not normalise them to LF on checkout. Without that override, the repo-wide
`* text=auto eol=lf` rule silently rewrites these files to LF on a fresh
clone or `git worktree add`, and the five golden tests above fail — the
working tree stays CRLF (and `git status` stays clean) only on the machine
that originally wrote them. See `issues/0078.md` for the full incident and
the deliberate choice of `-text` over normalising the comparison in
`goldenFile()`.

**Do not remove the `-text` override or convert these files to LF.** If you
regenerate them with `UPDATE_GOLDEN=1`, they will come back out as CRLF from
`renderText` on any platform, since the line ending is written by Go code,
not by an editor's newline setting.
