package clipboard

import (
	"strings"
	"testing"
)

func TestNormalizeCapturedTextTrimsTrailingLineEndings(t *testing.T) {
	t.Parallel()

	got, ok := normalizeCapturedText("hello world\r\n\n")
	if !ok {
		t.Fatal("expected normalized text")
	}
	if got != "hello world" {
		t.Fatalf("expected trimmed text, got %q", got)
	}
}

func TestNormalizeCapturedTextRejectsWhitespaceOnly(t *testing.T) {
	t.Parallel()

	if _, ok := normalizeCapturedText(" \t\r\n"); ok {
		t.Fatal("expected whitespace-only clipboard text to be rejected")
	}
}

func TestNormalizeCapturedTextPreservesInteriorNewlines(t *testing.T) {
	t.Parallel()

	got, ok := normalizeCapturedText("first line\nsecond line\r\n")
	if !ok {
		t.Fatal("expected normalized text")
	}
	if got != "first line\nsecond line" {
		t.Fatalf("expected interior newline to be preserved, got %q", got)
	}
}

func TestBuildWindowsImageCaptureScriptSeparatesStatements(t *testing.T) {
	t.Parallel()

	script := buildWindowsImageCaptureScript()
	if !strings.Contains(script, "\nif ($null -eq $img)") {
		t.Fatalf("expected Get-Clipboard assignment to be followed by a statement separator, got %s", script)
	}
	if !strings.Contains(script, "\n[Console]::Out.Write(") {
		t.Fatalf("expected image write call to be on its own statement, got %s", script)
	}
}
