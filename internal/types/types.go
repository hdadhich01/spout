package types

import "time"

// Line is a single parsed output line from a piped command.
type Line struct {
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}
