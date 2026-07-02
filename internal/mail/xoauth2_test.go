package mail

import "testing"

func TestXOAUTH2ClientStartFormat(t *testing.T) {
	c := newXOAUTH2Client("user@example.com", "ya29.token")
	mech, ir, err := c.Start()
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if mech != "XOAUTH2" {
		t.Errorf("unexpected mechanism: %q", mech)
	}
	want := "user=user@example.com\x01auth=Bearer ya29.token\x01\x01"
	if string(ir) != want {
		t.Errorf("unexpected initial response:\ngot:  %q\nwant: %q", ir, want)
	}
}

func TestXOAUTH2ClientNextReturnsEmptyNotError(t *testing.T) {
	c := newXOAUTH2Client("user@example.com", "token")
	resp, err := c.Next([]byte(`{"status":"400","schemes":"bearer"}`))
	if err != nil {
		t.Fatalf("expected no error on failure challenge, got: %v", err)
	}
	if len(resp) != 0 {
		t.Errorf("expected empty response, got: %q", resp)
	}
}
