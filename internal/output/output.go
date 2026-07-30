package output

import (
	"encoding/json"
	"fmt"
	"io"
)

const SchemaVersion = 1

type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

type Envelope struct {
	SchemaVersion int      `json:"schemaVersion"`
	OK            bool     `json:"ok"`
	OperationID   string   `json:"operationId"`
	Data          any      `json:"data,omitempty"`
	Warnings      []string `json:"warnings"`
	Error         *Error   `json:"error,omitempty"`
}

func JSON(w io.Writer, envelope Envelope) error {
	envelope.SchemaVersion = SchemaVersion
	if envelope.Warnings == nil {
		envelope.Warnings = []string{}
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(envelope)
}

func TextIssue(w io.Writer, number int, title, state string) {
	fmt.Fprintf(w, "#%d [%s] %s\n", number, state, title)
}
