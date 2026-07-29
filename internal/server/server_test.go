package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zoubingwu/gh-workbench/internal/model"
)

const testNotificationsSupported = true

func TestServerRequiresSessionAndSameOriginForCommands(t *testing.T) {
	t.Parallel()

	defaultPreferences := model.NotificationPreferences{
		OnlyMyPullRequests: true,
	}
	database := &fakeSnapshotStore{
		preferences: defaultPreferences,
		snapshot: model.Snapshot{
			RepositoryCount: 2,
			GeneratedAt:     time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC),
			Notifications:   defaultPreferences,
			Items:           make([]model.WorkItem, 0),
		},
	}
	controller := &fakeController{}
	server, err := New(
		database,
		controller,
		"github.com",
		"octocat",
		testNotificationsSupported,
		nil,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	handler := server.Handler()

	request := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	request.Host = "127.0.0.1:43123"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("bootstrap without session status = %d, want 401", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, server.SessionPath(), nil)
	request.Host = "127.0.0.1:43123"
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("session exchange status = %d, want 303", response.Code)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("session cookie count = %d, want 1", len(cookies))
	}

	request = httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
	request.Host = "127.0.0.1:43123"
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated bootstrap status = %d, want 200", response.Code)
	}
	if database.scope != "github.com" {
		t.Fatalf("snapshot scope = %q, want github.com", database.scope)
	}
	var snapshot model.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatalf("decode bootstrap: %v", err)
	}
	if snapshot.Host != "github.com" || snapshot.Viewer != "octocat" {
		t.Fatalf(
			"bootstrap account = %s@%s, want octocat@github.com",
			snapshot.Viewer,
			snapshot.Host,
		)
	}
	if snapshot.RepositoryCount != 2 {
		t.Fatalf(
			"bootstrap repository count = %d, want 2",
			snapshot.RepositoryCount,
		)
	}
	if snapshot.Notifications.Supported != testNotificationsSupported {
		t.Fatalf(
			"notification support = %t, want %t",
			snapshot.Notifications.Supported,
			testNotificationsSupported,
		)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/sync", nil)
	request.Host = "127.0.0.1:43123"
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-site protected sync status = %d, want 403", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/sync", nil)
	request.Host = "127.0.0.1:43123"
	request.Header.Set("Origin", "http://127.0.0.1:43123")
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("same-origin sync status = %d, want 202", response.Code)
	}
	if controller.triggers != 1 {
		t.Fatalf("sync trigger count = %d, want 1", controller.triggers)
	}

	request = httptest.NewRequest(
		http.MethodPatch,
		"/api/notifications",
		strings.NewReader(`{"enabled":true}`),
	)
	request.Host = "127.0.0.1:43123"
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-site notification settings status = %d, want 403", response.Code)
	}

	request = httptest.NewRequest(
		http.MethodPatch,
		"/api/notifications",
		strings.NewReader(`{"enabled":true,"unexpected":false}`),
	)
	request.Host = "127.0.0.1:43123"
	request.Header.Set("Origin", "http://127.0.0.1:43123")
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid notification settings status = %d, want 400", response.Code)
	}

	request = httptest.NewRequest(
		http.MethodPatch,
		"/api/notifications",
		strings.NewReader(`{"enabled":true,"onlyMyPullRequests":false}`),
	)
	request.Host = "127.0.0.1:43123"
	request.Header.Set("Origin", "http://127.0.0.1:43123")
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("multi-field notification settings status = %d, want 400", response.Code)
	}

	updates, unsubscribe := server.hub.subscribe()
	defer unsubscribe()

	request = httptest.NewRequest(
		http.MethodPatch,
		"/api/notifications",
		strings.NewReader(`{"enabled":true}`),
	)
	request.Host = "127.0.0.1:43123"
	request.Header.Set("Origin", "http://127.0.0.1:43123")
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("save notifications status = %d, want 200", response.Code)
	}
	var savedPreferences model.NotificationPreferences
	if err := json.NewDecoder(response.Body).Decode(&savedPreferences); err != nil {
		t.Fatalf("decode saved notification preferences: %v", err)
	}
	expectedPreferences := model.NotificationPreferences{
		Enabled:            true,
		OnlyMyPullRequests: true,
	}
	expectedResponse := expectedPreferences
	expectedResponse.Supported = testNotificationsSupported
	if savedPreferences != expectedResponse {
		t.Fatalf(
			"saved notification response = %#v, want %#v",
			savedPreferences,
			expectedResponse,
		)
	}
	if database.preferences != expectedPreferences {
		t.Fatalf(
			"saved preferences = %#v, want %#v",
			database.preferences,
			expectedPreferences,
		)
	}
	if database.saveCalls != 1 {
		t.Fatalf("notification preference saves = %d, want 1", database.saveCalls)
	}

	select {
	case payload := <-updates:
		var event struct {
			Type     string         `json:"type"`
			Snapshot model.Snapshot `json:"snapshot"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode notification update: %v", err)
		}
		if event.Type != "snapshot.updated" {
			t.Fatalf("notification update type = %q, want snapshot.updated", event.Type)
		}
		if event.Snapshot.Notifications != expectedResponse {
			t.Fatalf(
				"broadcast notification preferences = %#v, want %#v",
				event.Snapshot.Notifications,
				expectedResponse,
			)
		}
	case <-time.After(time.Second):
		t.Fatal("notification preference update was not broadcast")
	}
}

func TestServerRejectsNonLoopbackHost(t *testing.T) {
	t.Parallel()

	server, err := New(
		&fakeSnapshotStore{},
		&fakeController{},
		"github.com",
		"octocat",
		testNotificationsSupported,
		nil,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Host = "example.com"
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusMisdirectedRequest {
		t.Fatalf("non-loopback Host status = %d, want 421", response.Code)
	}
}

func TestServerSerializesNotificationSaveAndSnapshotPublication(t *testing.T) {
	t.Parallel()

	database := &serializationCheckingStore{}
	var (
		server                    *Server
		observerCalls             int
		unserializedObserverCalls int
	)
	server, err := New(
		database,
		&fakeController{},
		"github.com",
		"octocat",
		testNotificationsSupported,
		func(_ context.Context, _ model.Snapshot) {
			observerCalls++
			if server.publicationMu.TryLock() {
				unserializedObserverCalls++
				server.publicationMu.Unlock()
			}
		},
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	database.publicationMu = &server.publicationMu

	if _, err := server.PublishSnapshot(t.Context()); err != nil {
		t.Fatalf("PublishSnapshot() error = %v", err)
	}

	request := httptest.NewRequest(
		http.MethodPatch,
		"/api/notifications",
		strings.NewReader(`{"enabled":true}`),
	)
	request.Host = "127.0.0.1:43123"
	request.Header.Set("Origin", "http://127.0.0.1:43123")
	request.Header.Set("Content-Type", "application/json")
	request.AddCookie(&http.Cookie{
		Name:  server.cookieName,
		Value: server.session,
	})
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("save notifications status = %d, want 200", response.Code)
	}
	if database.unserializedCalls != 0 {
		t.Fatalf(
			"unserialized store calls = %d, want 0",
			database.unserializedCalls,
		)
	}
	if unserializedObserverCalls != 0 {
		t.Fatalf(
			"unserialized observer calls = %d, want 0",
			unserializedObserverCalls,
		)
	}
	if observerCalls != 1 {
		t.Fatalf("snapshot observer calls = %d, want 1", observerCalls)
	}
	if database.snapshotCalls != 2 || database.saveCalls != 1 {
		t.Fatalf(
			"store calls = %d snapshots, %d saves; want 2 snapshots, 1 save",
			database.snapshotCalls,
			database.saveCalls,
		)
	}
}

func TestServerServesEmbeddedIndexWithoutRedirect(t *testing.T) {
	t.Parallel()

	server, err := New(
		&fakeSnapshotStore{},
		&fakeController{},
		"github.com",
		"octocat",
		testNotificationsSupported,
		nil,
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Host = "127.0.0.1:43123"
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("index status = %d, want 200", response.Code)
	}
	if !strings.Contains(response.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("index body does not contain React root")
	}
}

func TestServerInstancesUseDistinctSessionCookies(t *testing.T) {
	t.Parallel()

	first, err := New(
		&fakeSnapshotStore{},
		&fakeController{},
		"github.com",
		"octocat",
		testNotificationsSupported,
		nil,
	)
	if err != nil {
		t.Fatalf("first New() error = %v", err)
	}
	second, err := New(
		&fakeSnapshotStore{},
		&fakeController{},
		"github.com",
		"hubot",
		testNotificationsSupported,
		nil,
	)
	if err != nil {
		t.Fatalf("second New() error = %v", err)
	}

	cookieName := func(server *Server) string {
		request := httptest.NewRequest(http.MethodGet, server.SessionPath(), nil)
		request.Host = "127.0.0.1:43123"
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, request)
		cookies := response.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("session cookie count = %d, want 1", len(cookies))
		}
		return cookies[0].Name
	}
	if firstName, secondName := cookieName(first), cookieName(second); firstName == secondName {
		t.Fatalf("session cookie names match: %q", firstName)
	}
}

type fakeSnapshotStore struct {
	snapshot    model.Snapshot
	preferences model.NotificationPreferences
	scope       string
	saveCalls   int
}

func (f *fakeSnapshotStore) Snapshot(
	_ context.Context,
	scope string,
	_ bool,
	_ time.Time,
) (model.Snapshot, error) {
	f.scope = scope
	return f.snapshot, nil
}

func (f *fakeSnapshotStore) UpdateNotificationPreferences(
	_ context.Context,
	update model.NotificationPreferencesUpdate,
) error {
	applyNotificationPreferencesUpdate(&f.preferences, update)
	applyNotificationPreferencesUpdate(&f.snapshot.Notifications, update)
	f.saveCalls++
	return nil
}

type serializationCheckingStore struct {
	publicationMu     *sync.Mutex
	snapshot          model.Snapshot
	snapshotCalls     int
	saveCalls         int
	unserializedCalls int
}

func (f *serializationCheckingStore) Snapshot(
	_ context.Context,
	_ string,
	_ bool,
	_ time.Time,
) (model.Snapshot, error) {
	f.recordSerialization()
	f.snapshotCalls++
	return f.snapshot, nil
}

func (f *serializationCheckingStore) UpdateNotificationPreferences(
	_ context.Context,
	update model.NotificationPreferencesUpdate,
) error {
	f.recordSerialization()
	applyNotificationPreferencesUpdate(&f.snapshot.Notifications, update)
	f.saveCalls++
	return nil
}

func applyNotificationPreferencesUpdate(
	preferences *model.NotificationPreferences,
	update model.NotificationPreferencesUpdate,
) {
	if update.Enabled != nil {
		preferences.Enabled = *update.Enabled
	}
	if update.OnlyMyPullRequests != nil {
		preferences.OnlyMyPullRequests = *update.OnlyMyPullRequests
	}
}

func (f *serializationCheckingStore) recordSerialization() {
	if f.publicationMu.TryLock() {
		f.unserializedCalls++
		f.publicationMu.Unlock()
	}
}

type fakeController struct {
	running  bool
	triggers int
}

func (f *fakeController) Trigger() {
	f.triggers++
}

func (f *fakeController) Running() bool {
	return f.running
}
