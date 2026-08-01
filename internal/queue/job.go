package queue

import "time"

// Image is a multimodal image payload for pi RPC prompts.
type Image struct {
	MimeType string
	Data     string // base64-encoded
}

// Job is a unit of work for the pi worker.
type Job struct {
	ID         string
	SessionKey string
	CWD        string
	Prompt     string
	Images     []Image
	CreatedAt  time.Time
	ChannelID  string // Discord reply routing
}
