package smtp

import "testing"

func TestXOAUTH2AuthStartFormat(t *testing.T) {
	auth := NewXOAUTH2Auth("user@example.com", "ya29.token")
	proto, toServer, err := auth.Start(nil)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if proto != "XOAUTH2" {
		t.Errorf("unexpected proto: %q", proto)
	}
	want := "user=user@example.com\x01auth=Bearer ya29.token\x01\x01"
	if string(toServer) != want {
		t.Errorf("unexpected toServer:\ngot:  %q\nwant: %q", toServer, want)
	}
}

func TestXOAUTH2AuthNextNoMoreReturnsNil(t *testing.T) {
	auth := NewXOAUTH2Auth("user@example.com", "token")
	resp, err := auth.Next(nil, false)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if resp != nil {
		t.Errorf("expected nil response when more=false, got: %q", resp)
	}
}

func TestXOAUTH2AuthNextWithMoreReturnsError(t *testing.T) {
	auth := NewXOAUTH2Auth("user@example.com", "token")
	_, err := auth.Next([]byte(`{"status":"400"}`), true)
	if err == nil {
		t.Fatal("expected error when server sends a failure challenge")
	}
}
