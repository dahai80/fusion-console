package server

import "testing"

// presenceStuckAlerts derives the "offline + queued" stuck state (issue #45
// F5): a member whose presence is not available while tasks are assigned to
// them must surface as an alert, never silently render as normal idle.
func TestPresenceStuckAlerts(t *testing.T) {
	roster := []map[string]any{
		{"name": "api-worker", "presence": "offline"},
		{"name": "docs-worker", "presence": "available"},
		{"name": "ops-worker", "presence": "paused"},
		{"name": "idle-worker"}, // no presence field — legacy daemon
	}
	tasks := []map[string]any{
		{"task_id": "t1", "assignee": "api-worker", "status": "queued"},
		{"task_id": "t2", "assignee": "api-worker", "status": "running"},
		{"task_id": "t3", "assignee": "docs-worker", "status": "queued"},
		{"task_id": "t4", "assignee": "ops-worker", "status": "done"},
		{"task_id": "t5", "assignee": "idle-worker", "status": "queued"},
	}
	alerts := presenceStuckAlerts(tasks, roster)
	if len(alerts) != 1 {
		t.Fatalf("alerts = %v, want exactly the api-worker stuck alert", alerts)
	}
	want := "member api-worker stuck: presence offline, 2 queued/running task(s)"
	if alerts[0] != want {
		t.Fatalf("alert = %q, want %q", alerts[0], want)
	}
}

// TestPresenceStuckAlertsEmpty: no members or no tasks — no alerts, no panic.
func TestPresenceStuckAlertsEmpty(t *testing.T) {
	if got := presenceStuckAlerts(nil, nil); len(got) != 0 {
		t.Fatalf("alerts = %v, want none", got)
	}
}
