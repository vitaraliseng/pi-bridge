package queue

import "time"

// Job is a unit of work for the pi worker.
type Job struct {
	ID         string
	SessionKey string
	CWD        string
	Prompt     string
	CreatedAt  time.Time

	// Discord reply routing
	ChannelID string
	MessageID string
	UserID    string
}
