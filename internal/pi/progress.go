package pi

import (
	"fmt"
	"strings"
)

// ProgressKind classifies a streaming progress update.
type ProgressKind string

const (
	// ProgressStatus is a high-level status line (starting, thinking, retrying…).
	ProgressStatus ProgressKind = "status"
	// ProgressToolStart means a tool began executing.
	ProgressToolStart ProgressKind = "tool_start"
	// ProgressToolEnd means a tool finished.
	ProgressToolEnd ProgressKind = "tool_end"
	// ProgressDelta is streamed assistant text.
	ProgressDelta ProgressKind = "delta"
)

// Progress is a user-facing update about what the agent is doing.
type Progress struct {
	Kind    ProgressKind
	Message string // human-readable summary for status/tool events
	Delta   string // text chunk for ProgressDelta
}

// FormatToolStart builds a short activity line for a tool invocation.
func FormatToolStart(name string, args map[string]any) string {
	switch name {
	case "bash":
		if cmd := stringArg(args, "command"); cmd != "" {
			return "Running shell: " + code(truncateRunes(cmd, 100))
		}
	case "read":
		if path := stringArg(args, "path"); path != "" {
			return "Reading " + code(path)
		}
	case "write":
		if path := stringArg(args, "path"); path != "" {
			return "Writing " + code(path)
		}
	case "edit":
		if path := stringArg(args, "path"); path != "" {
			return "Editing " + code(path)
		}
	case "grep":
		if pat := stringArg(args, "pattern"); pat != "" {
			path := stringArg(args, "path")
			if path == "" {
				path = stringArg(args, "glob")
			}
			if path != "" {
				return "Searching " + code(pat) + " in " + code(path)
			}
			return "Searching " + code(pat)
		}
	case "find", "ls":
		if path := stringArg(args, "path"); path != "" {
			return fmt.Sprintf("Listing %s %s", name, code(path))
		}
	}
	if name == "" {
		return "Using a tool"
	}
	return "Using tool " + code(name)
}

// FormatToolEnd builds a short completion line for a tool.
func FormatToolEnd(name string, isError bool) string {
	if name == "" {
		name = "tool"
	}
	if isError {
		return code(name) + " failed"
	}
	return "Finished " + code(name)
}

func stringArg(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

func code(s string) string {
	s = strings.ReplaceAll(s, "`", "'")
	return "`" + s + "`"
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if max < 1 || len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
