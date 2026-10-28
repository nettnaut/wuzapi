package main

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// History sync delivers batches that wuzapi saves from parallel goroutines,
// alongside live messages and API reads. On SQLite every save must succeed:
// a "database is locked" here silently drops a message from history.
func TestConcurrentHistoryWritesDoNotLock(t *testing.T) {
	db, err := initializeSQLite(DatabaseConfig{Path: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}
	s := &server{db: db}

	const writers, perWriter = 24, 300
	var wg sync.WaitGroup
	errs := make(chan error, writers*perWriter)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				id := fmt.Sprintf("W%d-%d", w, i)
				if err := s.saveMessageToHistory("u", fmt.Sprintf("chat%d", w%5), "s", id,
					"text", "hello", "", "", `{"Info":{}}`); err != nil {
					errs <- err
				}
				if i%50 == 0 { // readers interleaved, like API requests
					var n int
					if err := db.Get(&n, "SELECT COUNT(*) FROM message_history"); err != nil {
						errs <- err
					}
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	failed := 0
	var first error
	for err := range errs {
		if first == nil {
			first = err
		}
		failed++
	}
	if failed > 0 {
		t.Fatalf("%d of %d operations failed, first: %v", failed, writers*perWriter, first)
	}
	var n int
	if err := db.Get(&n, "SELECT COUNT(*) FROM message_history"); err != nil || n != writers*perWriter {
		t.Fatalf("rows = %d, %v", n, err)
	}
}

// A writer outside wuzapi's pool (another process, a long transaction) holds
// the lock for longer than busy_timeout: the save must retry, not drop the row.
func TestHistoryWriteRetriesPastBusyTimeout(t *testing.T) {
	old := sqliteBusyTimeoutMs
	sqliteBusyTimeoutMs = 100
	defer func() { sqliteBusyTimeoutMs = old }()
	dir := t.TempDir()
	db, err := initializeSQLite(DatabaseConfig{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := initializeSchema(db); err != nil {
		t.Fatal(err)
	}
	s := &server{db: db}

	other, err := initializeSQLite(DatabaseConfig{Path: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	tx, err := other.Beginx()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO message_history (user_id, chat_jid, sender_jid, message_id, timestamp, message_type) VALUES ('x','c','s','HOLD',CURRENT_TIMESTAMP,'text')"); err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(600 * time.Millisecond) // 6x the busy timeout
		tx.Commit()
		close(released)
	}()
	if err := s.saveMessageToHistory("u", "c", "s", "AFTER-LOCK", "text", "hi", "", "", "{}"); err != nil {
		t.Fatalf("save dropped while another writer held the lock: %v", err)
	}
	<-released
	var n int
	if err := db.Get(&n, "SELECT COUNT(*) FROM message_history WHERE message_id = 'AFTER-LOCK'"); err != nil || n != 1 {
		t.Fatalf("rows = %d, %v", n, err)
	}
}
