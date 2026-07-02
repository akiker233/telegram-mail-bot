package mail

import (
	"errors"
	"testing"

	pop3 "github.com/knadh/go-pop3"
)

func TestFilterUnseenSkipsSeenMessages(t *testing.T) {
	all := []pop3.MessageID{
		{ID: 1, UID: "uid-1"},
		{ID: 2, UID: "uid-2"},
		{ID: 3, UID: "uid-3"},
	}
	seen := map[string]bool{"uid-1": true, "uid-3": true}

	pending, err := filterUnseen(all, func(uidl string) (bool, error) {
		return seen[uidl], nil
	})
	if err != nil {
		t.Fatalf("filterUnseen returned error: %v", err)
	}
	if len(pending) != 1 || pending[0].UID != "uid-2" {
		t.Fatalf("expected only uid-2 to be pending, got %+v", pending)
	}
}

func TestFilterUnseenAllSeenReturnsEmpty(t *testing.T) {
	all := []pop3.MessageID{{ID: 1, UID: "uid-1"}, {ID: 2, UID: "uid-2"}}

	pending, err := filterUnseen(all, func(uidl string) (bool, error) {
		return true, nil
	})
	if err != nil {
		t.Fatalf("filterUnseen returned error: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("expected no pending messages, got %+v", pending)
	}
}

func TestFilterUnseenNoneSeenReturnsAll(t *testing.T) {
	all := []pop3.MessageID{{ID: 1, UID: "uid-1"}, {ID: 2, UID: "uid-2"}}

	pending, err := filterUnseen(all, func(uidl string) (bool, error) {
		return false, nil
	})
	if err != nil {
		t.Fatalf("filterUnseen returned error: %v", err)
	}
	if len(pending) != 2 {
		t.Fatalf("expected both messages pending, got %+v", pending)
	}
}

func TestFilterUnseenPropagatesStoreError(t *testing.T) {
	all := []pop3.MessageID{{ID: 1, UID: "uid-1"}}
	wantErr := errors.New("db error")

	_, err := filterUnseen(all, func(uidl string) (bool, error) {
		return false, wantErr
	})
	if err != wantErr {
		t.Fatalf("expected store error to propagate, got %v", err)
	}
}
