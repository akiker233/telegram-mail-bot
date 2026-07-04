package db

import (
	"testing"
	"time"
)

func TestHasSeenUIDAndMarkSeenUID(t *testing.T) {
	database := openTestDB(t)

	id, err := InsertAccount(database, &Account{
		TelegramUserID: 1, Label: "pop3-test", Email: "a@b.com",
		IMAPHost: "imap.b.com", IMAPPort: 993, IMAPUsername: "a@b.com",
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}

	seen, err := HasSeenUID(database, id, "uid-1")
	if err != nil {
		t.Fatalf("HasSeenUID returned error: %v", err)
	}
	if seen {
		t.Fatal("expected uid-1 to not be seen yet")
	}

	if err := MarkSeenUID(database, id, "uid-1"); err != nil {
		t.Fatalf("MarkSeenUID returned error: %v", err)
	}

	seen, err = HasSeenUID(database, id, "uid-1")
	if err != nil {
		t.Fatalf("HasSeenUID returned error: %v", err)
	}
	if !seen {
		t.Fatal("expected uid-1 to be seen after MarkSeenUID")
	}

	otherSeen, err := HasSeenUID(database, id, "uid-2")
	if err != nil {
		t.Fatalf("HasSeenUID returned error: %v", err)
	}
	if otherSeen {
		t.Fatal("expected uid-2 to remain unseen")
	}
}

func TestMarkSeenUIDIsIdempotent(t *testing.T) {
	database := openTestDB(t)

	id, err := InsertAccount(database, &Account{
		TelegramUserID: 1, Label: "pop3-test", Email: "a@b.com",
		IMAPHost: "imap.b.com", IMAPPort: 993, IMAPUsername: "a@b.com",
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := MarkSeenUID(database, id, "uid-1"); err != nil {
			t.Fatalf("MarkSeenUID call #%d returned error: %v", i+1, err)
		}
	}

	seen, err := HasSeenUID(database, id, "uid-1")
	if err != nil {
		t.Fatalf("HasSeenUID returned error: %v", err)
	}
	if !seen {
		t.Fatal("expected uid-1 to be seen")
	}
}

func TestPruneSeenUIDsRemovesOnlyOldRecords(t *testing.T) {
	database := openTestDB(t)

	id, err := InsertAccount(database, &Account{
		TelegramUserID: 1, Label: "pop3-test", Email: "a@b.com",
		IMAPHost: "imap.b.com", IMAPPort: 993, IMAPUsername: "a@b.com",
	})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}

	now := time.Now().UTC()
	old := now.Add(-40 * 24 * time.Hour).Format(time.RFC3339)
	recent := now.Add(-1 * time.Hour).Format(time.RFC3339)

	if _, err := database.Exec(
		`INSERT INTO pop3_seen_uids (account_id, uidl, seen_at) VALUES (?, ?, ?)`,
		id, "old-uid", old,
	); err != nil {
		t.Fatalf("insert old record returned error: %v", err)
	}
	if _, err := database.Exec(
		`INSERT INTO pop3_seen_uids (account_id, uidl, seen_at) VALUES (?, ?, ?)`,
		id, "recent-uid", recent,
	); err != nil {
		t.Fatalf("insert recent record returned error: %v", err)
	}

	if err := PruneSeenUIDs(database, id, now.Add(-30*24*time.Hour)); err != nil {
		t.Fatalf("PruneSeenUIDs returned error: %v", err)
	}

	oldSeen, err := HasSeenUID(database, id, "old-uid")
	if err != nil {
		t.Fatalf("HasSeenUID returned error: %v", err)
	}
	if oldSeen {
		t.Error("expected old-uid to be pruned")
	}

	recentSeen, err := HasSeenUID(database, id, "recent-uid")
	if err != nil {
		t.Fatalf("HasSeenUID returned error: %v", err)
	}
	if !recentSeen {
		t.Error("expected recent-uid to survive pruning")
	}
}

func TestCountSeenUIDs(t *testing.T) {
	database := openTestDB(t)

	id1, err := InsertAccount(database, &Account{TelegramUserID: 1, Label: "a1", Email: "a1@b.com", IMAPHost: "imap.b.com", IMAPPort: 993, IMAPUsername: "a1@b.com"})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}
	id2, err := InsertAccount(database, &Account{TelegramUserID: 1, Label: "a2", Email: "a2@b.com", IMAPHost: "imap.b.com", IMAPPort: 993, IMAPUsername: "a2@b.com"})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}

	if err := MarkSeenUID(database, id1, "uid-1"); err != nil {
		t.Fatalf("MarkSeenUID returned error: %v", err)
	}
	if err := MarkSeenUID(database, id1, "uid-2"); err != nil {
		t.Fatalf("MarkSeenUID returned error: %v", err)
	}
	if err := MarkSeenUID(database, id2, "uid-1"); err != nil {
		t.Fatalf("MarkSeenUID returned error: %v", err)
	}

	count1, err := CountSeenUIDs(database, id1)
	if err != nil {
		t.Fatalf("CountSeenUIDs returned error: %v", err)
	}
	if count1 != 2 {
		t.Fatalf("expected 2 seen uids for account 1, got %d", count1)
	}

	count2, err := CountSeenUIDs(database, id2)
	if err != nil {
		t.Fatalf("CountSeenUIDs returned error: %v", err)
	}
	if count2 != 1 {
		t.Fatalf("expected 1 seen uid for account 2, got %d", count2)
	}
}

func TestPruneSeenUIDsOnlyAffectsGivenAccount(t *testing.T) {
	database := openTestDB(t)

	id1, err := InsertAccount(database, &Account{TelegramUserID: 1, Label: "a1", Email: "a1@b.com", IMAPHost: "imap.b.com", IMAPPort: 993, IMAPUsername: "a1@b.com"})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}
	id2, err := InsertAccount(database, &Account{TelegramUserID: 1, Label: "a2", Email: "a2@b.com", IMAPHost: "imap.b.com", IMAPPort: 993, IMAPUsername: "a2@b.com"})
	if err != nil {
		t.Fatalf("InsertAccount returned error: %v", err)
	}

	old := time.Now().UTC().Add(-40 * 24 * time.Hour).Format(time.RFC3339)
	for _, id := range []int64{id1, id2} {
		if _, err := database.Exec(`INSERT INTO pop3_seen_uids (account_id, uidl, seen_at) VALUES (?, 'shared-uid', ?)`, id, old); err != nil {
			t.Fatalf("insert record for account %d returned error: %v", id, err)
		}
	}

	if err := PruneSeenUIDs(database, id1, time.Now().UTC()); err != nil {
		t.Fatalf("PruneSeenUIDs returned error: %v", err)
	}

	seen1, _ := HasSeenUID(database, id1, "shared-uid")
	if seen1 {
		t.Error("expected account 1's record to be pruned")
	}
	seen2, _ := HasSeenUID(database, id2, "shared-uid")
	if !seen2 {
		t.Error("expected account 2's record to survive, pruning should be scoped by account_id")
	}
}
