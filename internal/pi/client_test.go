package pi

import (
	"encoding/json"
	"testing"
)

func TestDispatchResponseCorrelatesByID(t *testing.T) {
	c := &Client{
		pending: make(map[string]chan Response),
		events:  make(chan Event, 4),
	}
	ch := make(chan Response, 1)
	c.pending["req-1"] = ch

	line, err := json.Marshal(map[string]any{
		"id":      "req-1",
		"type":    "response",
		"command": "prompt",
		"success": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	c.dispatch(line)

	select {
	case resp := <-ch:
		if !resp.Success || resp.Command != "prompt" {
			t.Fatalf("unexpected response: %+v", resp)
		}
	default:
		t.Fatal("expected correlated response")
	}
}

func TestDispatchEventGoesToChannel(t *testing.T) {
	c := &Client{
		pending: make(map[string]chan Response),
		events:  make(chan Event, 4),
	}
	line, err := json.Marshal(map[string]any{
		"type": "agent_settled",
	})
	if err != nil {
		t.Fatal(err)
	}
	c.dispatch(line)

	select {
	case ev := <-c.events:
		if ev["type"] != "agent_settled" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	default:
		t.Fatal("expected event")
	}
}
