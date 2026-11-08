//go:build integration

package learning

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onprs/ByebyeOCS/server/internal/web"
)

func TestAdminTaskDetailReadPermissionAndUserIsolation(t *testing.T) {
	pool := testPool(t)
	service, userID, taskID := prepareTask(t, pool, defaultFakePlatform())
	adminID := seedAdmin(t, pool)
	otherUser := seedUser(t, pool)
	server := newTestMux(t, service, map[string]Actor{
		"reader":   {ID: adminID, Kind: "admin", Permissions: []string{PermissionRead}},
		"operator": {ID: adminID, Kind: "admin", Permissions: []string{PermissionTasks}},
		"owner":    {ID: userID, Kind: "user"},
		"other":    {ID: otherUser, Kind: "user"},
	})
	for _, test := range []struct {
		actor, route string
		status       int
	}{
		{"reader", "/admin/api/v1/tasks/" + taskID, http.StatusOK},
		{"operator", "/admin/api/v1/tasks/" + taskID, http.StatusForbidden},
		{"owner", "/admin/api/v1/tasks/" + taskID, http.StatusForbidden},
		{"owner", "/api/v1/tasks/" + taskID, http.StatusOK},
		{"other", "/api/v1/tasks/" + taskID, http.StatusNotFound},
		{"reader", "/admin/api/v1/tasks/" + web.NewID(), http.StatusNotFound},
		{"reader", "/admin/api/v1/tasks/invalid", http.StatusNotFound},
		{"", "/admin/api/v1/tasks/" + taskID, http.StatusUnauthorized},
	} {
		t.Run(test.actor+test.route, func(t *testing.T) {
			response, body := requestJSON(t, http.MethodGet, server.URL+test.route, nil, map[string]string{"X-Test-Actor": test.actor})
			if response.StatusCode != test.status {
				t.Fatalf("得到 %d，期望 %d: %s", response.StatusCode, test.status, body)
			}
			if response.StatusCode == http.StatusOK {
				var detail TaskDetail
				if err := json.Unmarshal(body, &detail); err != nil {
					t.Fatal(err)
				}
				if detail.ID != taskID || detail.UserID != userID || detail.Attempts == nil || detail.Commands == nil {
					t.Fatalf("任务详情不完整: %+v", detail)
				}
			}
		})
	}
}
