package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/jmoiron/sqlx"
)

func TestSaveLocalMediaKeepsFileAfterTempIsRemoved(t *testing.T) {
	store, err := newMediaStore(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	media, out, err := store.create("MSG1.jpg", "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	out.WriteString("jpeg-bytes")
	out.Close()

	root := filepath.Join(t.TempDir(), "media")
	path, err := saveLocalMedia(root, media, media.Name)
	if err != nil {
		t.Fatal(err)
	}
	media.Close() // the pipeline removes its temp file; ours must survive
	if path != filepath.Join(root, "MSG1.jpg") {
		t.Fatalf("path = %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "jpeg-bytes" {
		t.Fatalf("content = %q, %v", got, err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}
	if leftovers, _ := filepath.Glob(filepath.Join(root, ".partial-*")); len(leftovers) != 0 {
		t.Fatalf("partial files left: %v", leftovers)
	}
	store.shutdown(context.Background())
}

func TestSaveLocalMediaStaysInsideRoot(t *testing.T) {
	store, err := newMediaStore(t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	media, out, _ := store.create("x", "")
	out.Close()
	defer media.Close()
	root := t.TempDir()
	path, err := saveLocalMedia(root, media, "../../escape.bin")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != root {
		t.Fatalf("escaped root: %s", path)
	}
	for _, bad := range []string{"", ".", ".."} {
		if _, err := saveLocalMedia(root, media, bad); err == nil {
			t.Fatalf("accepted name %q", bad)
		}
	}
}

func TestLocalMediaRootDefaultsUnderDataDir(t *testing.T) {
	oldMedia, oldData := *mediaDir, *dataDir
	defer func() { *mediaDir, *dataDir = oldMedia, oldData }()
	*mediaDir, *dataDir = "", "/srv/wuzapi"
	if got := localMediaRoot(); got != "/srv/wuzapi/media" {
		t.Fatalf("default root = %s", got)
	}
	*mediaDir = "/elsewhere"
	if got := localMediaRoot(); got != "/elsewhere" {
		t.Fatalf("explicit root = %s", got)
	}
}

func TestUnlimitedHistoryIsNeverTrimmed(t *testing.T) {
	s := makeTestServer(t)
	for _, id := range []string{"A", "B", "C"} {
		if err := s.saveMessageToHistory("u", "chat", "s", id, "text", id, "", "", "{}"); err != nil {
			t.Fatal(err)
		}
	}
	count := func() (n int) {
		t.Helper()
		if err := s.db.Get(&n, "SELECT COUNT(*) FROM message_history WHERE user_id = 'u'"); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := s.trimMessageHistory("u", "chat", -1); err != nil || count() != 3 {
		t.Fatalf("unlimited trimmed: %d rows, %v", count(), err)
	}
	if err := s.trimMessageHistory("u", "chat", 1); err != nil || count() != 1 {
		t.Fatalf("limit 1 kept %d rows, %v", count(), err)
	}
	if historyEnabled(0) || !historyEnabled(-1) || !historyEnabled(5) {
		t.Fatal("historyEnabled: 0 must be off, -1 and positive on")
	}
}

func TestSetHistoryAcceptsUnlimitedRejectsBelow(t *testing.T) {
	historyDrivers(t, func(t *testing.T, db *sqlx.DB) {
		if err := initializeSchema(db); err != nil {
			t.Fatal(err)
		}
		s := &server{db: db}
		result := historyRequest(t, s.AddUser(), "POST", `{"name":"Unlimited","token":"history-test-token","history":10}`, "")
		id := result["data"].(map[string]interface{})["id"].(string)
		historyRequest(t, s.SetHistory(), "POST", `{"history":-1}`, id)
		var got int
		if err := db.QueryRow(db.Rebind("SELECT history FROM users WHERE id=?"), id).Scan(&got); err != nil || got != -1 {
			t.Fatalf("history = %d, %v", got, err)
		}
		req := httptest.NewRequest("POST", "/", strings.NewReader(`{"history":-2}`))
		req = mux.SetURLVars(req, map[string]string{"id": id})
		req = req.WithContext(context.WithValue(req.Context(), "userinfo", Values{m: map[string]string{"Id": id}}))
		rec := httptest.NewRecorder()
		s.SetHistory()(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("accepted history -2: %d", rec.Code)
		}
	})
}
