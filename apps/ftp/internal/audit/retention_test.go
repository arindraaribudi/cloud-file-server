package audit

import (
	"strings"
	"testing"
	"time"
)

func TestDropOldPartitionsSQL(t *testing.T) {
	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	stmt := DropOldPartitionsSQL("audit_events", cutoff)
	if !strings.Contains(stmt, "audit_events") {
		t.Errorf("missing table: %s", stmt)
	}
}