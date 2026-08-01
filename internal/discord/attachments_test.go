package discord

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestSanitizeFilename(t *testing.T) {
	if got := sanitizeFilename("../../etc/passwd", 0); got != "passwd" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizeFilename("", 2); got != "attachment-3" {
		t.Fatalf("got %q", got)
	}
}

func TestIsTextLike(t *testing.T) {
	if !isTextLike("text/plain", "x.bin") {
		t.Fatal("text/plain should be text")
	}
	if !isTextLike("application/json", "data.bin") {
		t.Fatal("json mime should be text")
	}
	if !isTextLike("application/octet-stream", "notes.md") {
		t.Fatal(".md should be text")
	}
	if isTextLike("application/octet-stream", "blob.bin") {
		t.Fatal("bin should not be text")
	}
}

func TestPrepareAttachmentsInlinesTextAndImages(t *testing.T) {
	textBody := "hello from attachment\nline 2"
	imgBody := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a} // PNG magic

	mux := http.NewServeMux()
	mux.HandleFunc("/text.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(textBody))
	})
	mux.HandleFunc("/pic.png", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(imgBody)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	tmp := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmp)

	got, err := prepareAttachments("msg-1", []*discordgo.MessageAttachment{
		{ID: "1", Filename: "text.txt", URL: srv.URL + "/text.txt", ContentType: "text/plain", Size: len(textBody)},
		{ID: "2", Filename: "pic.png", URL: srv.URL + "/pic.png", ContentType: "image/png", Size: len(imgBody)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.PromptExtra, textBody) {
		t.Fatalf("expected inlined text, got:\n%s", got.PromptExtra)
	}
	if !strings.Contains(got.PromptExtra, "text.txt") {
		t.Fatalf("expected filename in prompt, got:\n%s", got.PromptExtra)
	}
	if len(got.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(got.Images))
	}
	if got.Images[0].MimeType != "image/png" {
		t.Fatalf("mime: %s", got.Images[0].MimeType)
	}
	decoded, err := base64.StdEncoding.DecodeString(got.Images[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(imgBody) {
		t.Fatalf("image bytes mismatch")
	}

	var found bool
	_ = filepath.Walk(tmp, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(path, "text.txt") {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("expected saved attachment under XDG_CONFIG_HOME")
	}
}
