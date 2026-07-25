package pi

import "testing"

func TestFormatToolStart(t *testing.T) {
	got := FormatToolStart("bash", map[string]any{"command": "ls -la"})
	if got != "Running shell: `ls -la`" {
		t.Fatalf("got %q", got)
	}
	got = FormatToolStart("read", map[string]any{"path": "main.go"})
	if got != "Reading `main.go`" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatToolEnd(t *testing.T) {
	if FormatToolEnd("bash", false) != "Finished `bash`" {
		t.Fatal(FormatToolEnd("bash", false))
	}
	if FormatToolEnd("bash", true) != "`bash` failed" {
		t.Fatal(FormatToolEnd("bash", true))
	}
}
