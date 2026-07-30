package adapterdocs

import (
	"strings"
	"testing"
)

func TestMVPAcceptanceRecordTracksEveryCriterion(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	record := readRepositoryFile(t, root, "docs/mvp-acceptance.md")
	for number := 1; number <= 13; number++ {
		marker := "| " + decimal(number) + " |"
		if !strings.Contains(record, marker) {
			t.Errorf("acceptance record is missing criterion %d", number)
		}
	}
	if !strings.Contains(record, "Codex CLI 0.146.0") ||
		!strings.Contains(record, "| Pass |") ||
		!strings.Contains(record, "skill-forward-test.md") {
		t.Error("acceptance record must retain the successful fresh-session evidence")
	}
}

func decimal(value int) string {
	if value < 10 {
		return string(rune('0' + value))
	}
	return "1" + string(rune('0'+value-10))
}
