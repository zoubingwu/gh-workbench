package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/zoubingwu/gh-workbench/internal/model"
	"github.com/zoubingwu/gh-workbench/internal/notification"
	"github.com/zoubingwu/gh-workbench/internal/webui"
)

const (
	sessionCookiePrefix = "gh_workbench_"
	writeTimeout        = 5 * time.Second
	maxJSONBodyBytes    = 1024
)

type SnapshotStore interface {
	Snapshot(
		context.Context,
		string,
		bool,
		time.Time,
	) (model.Snapshot, error)
	SaveNotificationPreferences(
		context.Context,
		model.NotificationPreferences,
	) error
}

type SyncController interface {
	Trigger()
	Running() bool
}

type Server struct {
	store      SnapshotStore
	controller SyncController
	host       string
	viewer     string
	session    string
	cookieName string
	ui         fs.FS
	hub        *hub
	handler    http.Handler
}

func New(
	store SnapshotStore,
	controller SyncController,
	host string,
	viewer string,
) (*Server, error) {
	session, err := newSession()
	if err != nil {
		return nil, err
	}
	ui, err := webui.FS()
	if err != nil {
		return nil, fmt.Errorf("open embedded web UI: %w", err)
	}

	server := &Server{
		store:      store,
		controller: controller,
		host:       host,
		viewer:     viewer,
		session:    session,
		cookieName: sessionCookiePrefix + session[:16],
		ui:         ui,
		hub:        newHub(),
	}
	server.handler = server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) SessionPath() string {
	return "/session/" + s.session
}

func (s *Server) PublishSnapshot(
	ctx context.Context,
) (model.Snapshot, error) {
	snapshot, err := s.snapshot(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}
	if err := s.hub.publish(snapshot); err != nil {
		return model.Snapshot{}, fmt.Errorf("publish snapshot: %w", err)
	}
	return snapshot, nil
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /session/{token}", s.handleSession)
	mux.HandleFunc("GET /api/bootstrap", s.handleBootstrap)
	mux.HandleFunc("POST /api/sync", s.handleSync)
	mux.HandleFunc("PUT /api/notifications", s.handleNotifications)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("/", s.handleStatic)

	return securityHeaders(loopbackHost(mux))
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.session)) != 1 {
		http.NotFound(w, r)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName,
		Value:    s.session,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if !s.authenticated(r) {
		writeError(w, http.StatusUnauthorized, "session required")
		return
	}
	snapshot, err := s.snapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load workbench snapshot")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	if !s.authenticated(r) {
		writeError(w, http.StatusUnauthorized, "session required")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "same-origin request required")
		return
	}

	s.controller.Trigger()
	writeJSON(w, http.StatusAccepted, struct {
		Accepted bool `json:"accepted"`
	}{Accepted: true})
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if !s.authenticated(r) {
		writeError(w, http.StatusUnauthorized, "session required")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "same-origin request required")
		return
	}

	var preferences model.NotificationPreferences
	if err := decodeJSON(w, r, &preferences); err != nil {
		writeError(w, http.StatusBadRequest, "invalid notification settings")
		return
	}
	if err := s.store.SaveNotificationPreferences(
		r.Context(),
		preferences,
	); err != nil {
		writeError(w, http.StatusInternalServerError, "save notification settings")
		return
	}
	preferences.Supported = notification.Supported
	writeJSON(w, http.StatusOK, preferences)
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if !s.authenticated(r) {
		writeError(w, http.StatusUnauthorized, "session required")
		return
	}
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "same-origin request required")
		return
	}

	connection, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer connection.CloseNow()

	connection.SetReadLimit(1024)
	clientContext := connection.CloseRead(context.Background())
	updates, unsubscribe := s.hub.subscribe()
	defer unsubscribe()
	publishContext, publishCancel := context.WithTimeout(
		clientContext,
		writeTimeout,
	)
	_, err = s.PublishSnapshot(publishContext)
	publishCancel()
	if err != nil {
		return
	}

	for {
		select {
		case <-clientContext.Done():
			return
		case payload, ok := <-updates:
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(clientContext, writeTimeout)
			err := connection.Write(ctx, websocket.MessageText, payload)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/session/") {
		http.NotFound(w, r)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	info, err := fs.Stat(s.ui, path)
	if err != nil || info.IsDir() {
		path = "index.html"
		info, err = fs.Stat(s.ui, path)
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if strings.HasPrefix(path, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	content, err := fs.ReadFile(s.ui, path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, path, info.ModTime(), bytes.NewReader(content))
}

func (s *Server) snapshot(ctx context.Context) (model.Snapshot, error) {
	snapshot, err := s.store.Snapshot(
		ctx,
		s.host,
		s.controller.Running(),
		time.Now().UTC(),
	)
	if err != nil {
		return model.Snapshot{}, err
	}
	snapshot.Host = s.host
	snapshot.Viewer = s.viewer
	snapshot.Notifications.Supported = notification.Supported
	if snapshot.Items == nil {
		snapshot.Items = make([]model.WorkItem, 0)
	}
	return snapshot, nil
}

func (s *Server) authenticated(r *http.Request) bool {
	cookie, err := r.Cookie(s.cookieName)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(s.session)) == 1
}

func newSession() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate local session: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func sameOrigin(r *http.Request) bool {
	rawOrigin := r.Header.Get("Origin")
	origin, err := url.Parse(rawOrigin)
	if err != nil || origin.Scheme == "" || origin.Host == "" {
		return false
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return false
	}
	return strings.EqualFold(origin.Host, r.Host)
}

func loopbackHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if parsed, _, err := net.SplitHostPort(r.Host); err == nil {
			host = parsed
		}
		host = strings.Trim(host, "[]")

		ip := net.ParseIP(host)
		if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
			http.Error(w, "loopback Host required", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; connect-src 'self' ws:; img-src 'self' data: https:; "+
				"style-src 'self' 'unsafe-inline'; script-src 'self'; "+
				"base-uri 'none'; frame-ancestors 'none'",
		)
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{Error: message})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(
		http.MaxBytesReader(w, r.Body, maxJSONBodyBytes),
	)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	err := decoder.Decode(&struct{}{})
	if err == nil {
		return errors.New("decode trailing json: multiple values")
	}
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode trailing json: %w", err)
	}
	return nil
}
