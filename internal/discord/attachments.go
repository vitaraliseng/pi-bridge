package discord

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/discordgo"

	"github.com/vitaraliseng/pi-bridge/internal/queue"
)

const (
	maxAttachmentBytes = 20 << 20  // 20 MiB
	maxInlineTextBytes = 200 << 10 // 200 KiB inlined into the prompt
)

var attachmentHTTPClient = &http.Client{Timeout: 60 * time.Second}

// preparedAttachments is the local materialization of Discord message attachments.
type preparedAttachments struct {
	PromptExtra string
	Images      []queue.Image
}

func prepareAttachments(messageID string, atts []*discordgo.MessageAttachment) (preparedAttachments, error) {
	var out preparedAttachments
	if len(atts) == 0 {
		return out, nil
	}

	dir, err := attachmentDir(messageID)
	if err != nil {
		return out, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return out, fmt.Errorf("create attachment dir: %w", err)
	}

	var b strings.Builder
	b.WriteString("\n\n---\nDiscord attachments saved locally for this message:\n")

	for i, att := range atts {
		if att == nil {
			continue
		}
		name := sanitizeFilename(att.Filename, i)
		path := filepath.Join(dir, name)

		if att.Size > maxAttachmentBytes {
			fmt.Fprintf(&b, "\n- %s: skipped (declared size %d > %d bytes)\n", name, att.Size, maxAttachmentBytes)
			continue
		}

		data, err := downloadAttachment(att.URL)
		if err != nil {
			fmt.Fprintf(&b, "\n- %s: download failed: %v\n", name, err)
			continue
		}
		if len(data) > maxAttachmentBytes {
			fmt.Fprintf(&b, "\n- %s: skipped (downloaded size %d > %d bytes)\n", name, len(data), maxAttachmentBytes)
			continue
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			fmt.Fprintf(&b, "\n- %s: write failed: %v\n", name, err)
			continue
		}

		mime := strings.TrimSpace(att.ContentType)
		if mime == "" {
			mime = http.DetectContentType(data)
		}
		// Strip parameters (e.g. charset).
		if i := strings.IndexByte(mime, ';'); i >= 0 {
			mime = strings.TrimSpace(mime[:i])
		}

		fmt.Fprintf(&b, "\n- file: %s\n  path: %s\n  type: %s\n  size: %d\n", name, path, mime, len(data))

		switch {
		case isImageMIME(mime):
			out.Images = append(out.Images, queue.Image{
				MimeType: mime,
				Data:     base64.StdEncoding.EncodeToString(data),
			})
			b.WriteString("  note: also attached as an image for multimodal models\n")
		case isTextLike(mime, name) && len(data) <= maxInlineTextBytes && utf8.Valid(data):
			b.WriteString("  content:\n```\n")
			b.Write(data)
			if len(data) > 0 && data[len(data)-1] != '\n' {
				b.WriteByte('\n')
			}
			b.WriteString("```\n")
		default:
			b.WriteString("  note: binary/non-text; open the path with tools if needed\n")
		}
	}

	out.PromptExtra = b.String()
	return out, nil
}

func attachmentDir(messageID string) (string, error) {
	// Match pi-bridge config layout: $XDG_CONFIG_HOME/pi-bridge or ~/.config/pi-bridge.
	var base string
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		base = filepath.Join(xdg, "pi-bridge")
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home: %w", err)
		}
		base = filepath.Join(home, ".config", "pi-bridge")
	}
	id := sanitizeFilename(messageID, 0)
	return filepath.Join(base, "attachments", id), nil
}

func downloadAttachment(rawURL string) ([]byte, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, fmt.Errorf("empty attachment url")
	}
	resp, err := attachmentHTTPClient.Get(rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxAttachmentBytes+1))
}

func sanitizeFilename(name string, idx int) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, string(filepath.Separator), "_")
	if name == "" || name == "." || name == ".." {
		return fmt.Sprintf("attachment-%d", idx+1)
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return '_'
		}
		return r
	}, name)
	return name
}

func isImageMIME(mime string) bool {
	switch strings.ToLower(mime) {
	case "image/png", "image/jpeg", "image/jpg", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}

func isTextLike(mime, filename string) bool {
	mime = strings.ToLower(mime)
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	switch mime {
	case "application/json", "application/xml", "application/yaml",
		"application/x-yaml", "application/toml", "application/javascript",
		"application/typescript", "application/sql", "application/x-sh",
		"application/x-python", "application/csv":
		return true
	}
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".txt", ".md", ".markdown", ".csv", ".tsv", ".json", ".jsonl",
		".yaml", ".yml", ".toml", ".xml", ".html", ".htm", ".css",
		".js", ".jsx", ".ts", ".tsx", ".go", ".py", ".rb", ".rs", ".java",
		".c", ".h", ".cpp", ".hpp", ".cs", ".sh", ".bash", ".zsh", ".fish",
		".env", ".ini", ".cfg", ".conf", ".sql", ".graphql", ".proto",
		".swift", ".kt", ".kts", ".scala", ".lua", ".r", ".php", ".pl",
		".dockerfile", ".makefile", ".cmake", ".gradle", ".properties",
		".log", ".diff", ".patch":
		return true
	default:
		return false
	}
}
