package lsp

import "testing"

// ApplyEdits is the byte-level contract behind lsp_organize_imports: the
// server proposes, these functions write — so clamping, back-to-front
// splicing, and LSP's UTF-16 columns are pinned here without a server.

func TestApplyEditsInsertReplaceAndMulti(t *testing.T) {
	const doc = "import \"b\";\nimport \"a\";\nexport const x = 1\n"

	if got := ApplyEdits(doc, nil); got != doc {
		t.Errorf("no edits must be byte-identical, got %q", got)
	}

	// Insert at the very start (imports go to the top).
	got := ApplyEdits(doc, []TextEdit{{StartLine: 1, StartCol: 1, EndLine: 1, EndCol: 1,
		NewText: "import \"z\";\n"}})
	if want := "import \"z\";\n" + doc; got != want {
		t.Errorf("insert = %q, want %q", got, want)
	}

	// Delete line 2 entirely (line + newline): end position is line 3 col 1.
	got = ApplyEdits(doc, []TextEdit{{StartLine: 2, StartCol: 1, EndLine: 3, EndCol: 1}})
	if want := "import \"b\";\nexport const x = 1\n"; got != want {
		t.Errorf("delete line = %q, want %q", got, want)
	}

	// Multiple edits splice back to front — both land despite shifting.
	got = ApplyEdits(doc, []TextEdit{
		{StartLine: 1, StartCol: 1, EndLine: 1, EndCol: 1, NewText: "A\n"},
		{StartLine: 3, StartCol: 1, EndLine: 3, EndCol: 1, NewText: "C\n"},
	})
	if want := "A\nimport \"b\";\nimport \"a\";\nC\nexport const x = 1\n"; got != want {
		t.Errorf("multi = %q, want %q", got, want)
	}
}

// A server quoting stale positions must clamp to the document, never
// corrupt or panic — the write path degrades safely.
func TestApplyEditsClampsOutOfBounds(t *testing.T) {
	const doc = "one\ntwo\n"
	got := ApplyEdits(doc, []TextEdit{{StartLine: 99, StartCol: 1, EndLine: 99, EndCol: 1,
		NewText: "tail"}})
	if want := doc + "tail"; got != want {
		t.Errorf("past-EOF line = %q, want %q", got, want)
	}
	got = ApplyEdits(doc, []TextEdit{{StartLine: 1, StartCol: 1, EndLine: 1, EndCol: 500,
		NewText: "ONE"}})
	if want := "ONE\ntwo\n"; got != want {
		t.Errorf("past-line column = %q, want %q (clamped to the newline)", got, want)
	}
}

// LSP columns are UTF-16 units: a non-BMP rune counts two, so an edit
// after an emoji lands after it — byte-naive counting would slice the
// character in half.
func TestApplyEditsCountsUTF16Columns(t *testing.T) {
	const doc = "🙂x\n"
	got := ApplyEdits(doc, []TextEdit{{StartLine: 1, StartCol: 3, EndLine: 1, EndCol: 3,
		NewText: "Y"}})
	if want := "🙂Yx\n"; got != want {
		t.Errorf("astral insert = %q, want %q (col 3 = after the 2-unit rune)", got, want)
	}
}
