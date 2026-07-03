package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"testing"
)

func buildZip(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create("mailbot_windows_amd64/" + name)
	if err != nil {
		t.Fatalf("failed to create zip entry: %v", err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatalf("failed to write zip entry: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("failed to close zip writer: %v", err)
	}
	return buf.Bytes()
}

func buildTarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	header := &tar.Header{
		Name: "mailbot_linux_amd64/" + name,
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatalf("failed to write tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("failed to write tar content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("failed to close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
	return buf.Bytes()
}

func TestExtractBinaryFromZip(t *testing.T) {
	want := []byte("fake windows binary content")
	data := buildZip(t, "mailbot.exe", want)

	got, err := extractBinary(data, "windows")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted content mismatch, got %q, want %q", got, want)
	}
}

func TestExtractBinaryFromTarGz(t *testing.T) {
	want := []byte("fake linux binary content")
	data := buildTarGz(t, "mailbot", want)

	got, err := extractBinary(data, "linux")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted content mismatch, got %q, want %q", got, want)
	}
}

func TestExtractBinaryNotFound(t *testing.T) {
	data := buildZip(t, "other-file.txt", []byte("irrelevant"))

	if _, err := extractBinary(data, "windows"); err == nil {
		t.Error("expected error when binary is missing from archive, got nil")
	}
}
