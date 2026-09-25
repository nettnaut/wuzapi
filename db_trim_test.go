package main

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
)

// In SQLite mode the whatsmeow store (main.db, holding
// whatsmeow_message_secrets) and wuzapi's own tables (users.db, holding
// message_history) are separate files. trimMessageHistory used to query
// whatsmeow_message_secrets on the users.db handle, fail with "no such
// table", and return before trimming history — so the per-user history
// limit was never enforced on SQLite.

func openTrimTestDB(t *testing.T, name string) *sqlx.DB {
	t.Helper()
	db, err := sqlx.Open("sqlite", filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// seedTrimHistory inserts n messages for chat with strictly increasing
// timestamps (msg-0 oldest) and returns their IDs.
func seedTrimHistory(t *testing.T, s *server, userID, chat string, n int) []string {
	t.Helper()
	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		ids[i] = fmt.Sprintf("msg-%d", i)
		if _, err := s.db.Exec(`INSERT INTO message_history
			(user_id, chat_jid, sender_jid, message_id, timestamp, message_type, text_content)
			VALUES (?, ?, ?, ?, ?, 'text', 'hi')`,
			userID, chat, chat, ids[i], base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	return ids
}

func countRows(t *testing.T, db *sqlx.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Get(&n, query, args...); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTrimMessageHistorySQLiteSeparateStore(t *testing.T) {
	const userID, chat = "user-1", "123@s.whatsapp.net"

	users := openTrimTestDB(t, "users.db")
	if err := initializeSchema(users); err != nil {
		t.Fatal(err)
	}
	store := openTrimTestDB(t, "main.db")
	if _, err := store.Exec(`CREATE TABLE whatsmeow_message_secrets
		(chat_jid TEXT, sender_jid TEXT, message_id TEXT)`); err != nil {
		t.Fatal(err)
	}

	s := &server{db: users, storeDB: store}
	ids := seedTrimHistory(t, s, userID, chat, 5)
	for _, id := range ids {
		if _, err := store.Exec(`INSERT INTO whatsmeow_message_secrets VALUES (?, ?, ?)`,
			chat, chat, id); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.trimMessageHistory(userID, chat, 2); err != nil {
		t.Fatalf("trim failed: %v", err)
	}

	if n := countRows(t, users, `SELECT COUNT(*) FROM message_history`); n != 2 {
		t.Fatalf("expected 2 history rows after trim, got %d", n)
	}
	// The two newest messages survive, in both stores.
	for _, id := range ids[3:] {
		if n := countRows(t, users, `SELECT COUNT(*) FROM message_history WHERE message_id = ?`, id); n != 1 {
			t.Fatalf("newest message %s was trimmed from history", id)
		}
		if n := countRows(t, store, `SELECT COUNT(*) FROM whatsmeow_message_secrets WHERE message_id = ?`, id); n != 1 {
			t.Fatalf("secret for kept message %s was deleted", id)
		}
	}
	if n := countRows(t, store, `SELECT COUNT(*) FROM whatsmeow_message_secrets`); n != 2 {
		t.Fatalf("expected 2 secrets after trim, got %d", n)
	}
}

func TestTrimMessageHistoryWithoutStoreStillTrims(t *testing.T) {
	const userID, chat = "user-1", "123@s.whatsapp.net"

	users := openTrimTestDB(t, "users.db")
	if err := initializeSchema(users); err != nil {
		t.Fatal(err)
	}
	s := &server{db: users} // no store handle: secrets are skipped, history is not
	seedTrimHistory(t, s, userID, chat, 5)

	if err := s.trimMessageHistory(userID, chat, 3); err != nil {
		t.Fatalf("trim failed: %v", err)
	}
	if n := countRows(t, users, `SELECT COUNT(*) FROM message_history`); n != 3 {
		t.Fatalf("expected 3 history rows after trim, got %d", n)
	}
}

func TestTrimMessageHistoryOtherChatsUntouched(t *testing.T) {
	const userID = "user-1"

	users := openTrimTestDB(t, "users.db")
	if err := initializeSchema(users); err != nil {
		t.Fatal(err)
	}
	s := &server{db: users}
	seedTrimHistory(t, s, userID, "a@s.whatsapp.net", 5)
	if _, err := users.Exec(`INSERT INTO message_history
		(user_id, chat_jid, sender_jid, message_id, timestamp, message_type, text_content)
		VALUES (?, 'b@s.whatsapp.net', 'b@s.whatsapp.net', 'b-1', ?, 'text', 'hi')`,
		userID, time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := s.trimMessageHistory(userID, "a@s.whatsapp.net", 1); err != nil {
		t.Fatalf("trim failed: %v", err)
	}
	if n := countRows(t, users, `SELECT COUNT(*) FROM message_history WHERE chat_jid = 'b@s.whatsapp.net'`); n != 1 {
		t.Fatalf("trim of chat a touched chat b")
	}
}
