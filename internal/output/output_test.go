package output

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestJSONEnvelopeHasStableShape(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	if err := JSON(&buffer, Envelope{OK: true, OperationID: "op_test", Data: map[string]int{"n": 1}}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["schemaVersion"] != float64(1) || got["ok"] != true || got["operationId"] != "op_test" {
		t.Fatalf("unexpected envelope: %v", got)
	}
	warnings, ok := got["warnings"].([]any)
	if !ok || len(warnings) != 0 {
		t.Fatalf("warnings must be an empty array: %T %v", got["warnings"], got["warnings"])
	}
}
