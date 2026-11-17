package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAttachment(t *testing.T, s *Server, stored, content string) {
	t.Helper()
	dir := filepath.Join(s.cfgDir, "attachments")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, stored), []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestReadAttachmentSegmentChunksAndEof(t *testing.T) {
	s := &Server{cfgDir: t.TempDir()}
	content := strings.Repeat("a", 100)
	writeAttachment(t, s, "1-note.txt", content)

	seg, err := s.readAttachmentSegment("1-note.txt", 0, 40)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if seg.BytesRead != 40 || seg.NextOffset != 40 || seg.EOF {
		t.Fatalf("first segment: read=%d next=%d eof=%v", seg.BytesRead, seg.NextOffset, seg.EOF)
	}
	if seg.Content != strings.Repeat("a", 40) || seg.Encoding != "text" {
		t.Fatalf("first segment content wrong: enc=%s len=%d", seg.Encoding, len(seg.Content))
	}

	seg, err = s.readAttachmentSegment("1-note.txt", 40, 0) // 0 -> default 64 KB
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if seg.BytesRead != 60 || seg.NextOffset != -1 || !seg.EOF {
		t.Fatalf("last segment: read=%d next=%d eof=%v", seg.BytesRead, seg.NextOffset, seg.EOF)
	}
}

func TestReadAttachmentSegmentBinaryIsBase64(t *testing.T) {
	s := &Server{cfgDir: t.TempDir()}
	dir := filepath.Join(s.cfgDir, "attachments")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw := []byte{0xff, 0xfe, 0x00, 0x01}
	if err := os.WriteFile(filepath.Join(dir, "2-bin.dat"), raw, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	seg, err := s.readAttachmentSegment("2-bin.dat", 0, 64)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if seg.Encoding != "base64" {
		t.Fatalf("encoding = %s, want base64", seg.Encoding)
	}
	decoded, decErr := base64.StdEncoding.DecodeString(seg.Content)
	if decErr != nil || string(decoded) != string(raw) {
		t.Fatalf("base64 round-trip failed: %v", decErr)
	}
}

func TestReadAttachmentSegmentRejectsTraversalAndMissing(t *testing.T) {
	s := &Server{cfgDir: t.TempDir()}
	if _, err := s.readAttachmentSegment("../../secrets.txt", 0, 64); err == nil {
		t.Fatal("traversal name must fail (Base strips it to an unknown file)")
	}
	if _, err := s.readAttachmentSegment("nope.txt", 0, 64); err == nil {
		t.Fatal("missing attachment must fail")
	}
	// empty name -> Base(".") -> rejected
	if _, err := s.readAttachmentSegment("", 0, 64); err == nil {
		t.Fatal("empty name must fail")
	}
}

func TestReadAttachmentSegmentNegativeOffsetClamped(t *testing.T) {
	s := &Server{cfgDir: t.TempDir()}
	writeAttachment(t, s, "3-x.txt", "hello")
	seg, err := s.readAttachmentSegment("3-x.txt", -5, 64)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if seg.Offset != 0 || seg.Content != "hello" {
		t.Fatalf("offset=%d content=%q", seg.Offset, seg.Content)
	}
}

func TestHandleAttachmentReadHTTP(t *testing.T) {
	s := &Server{cfgDir: t.TempDir()}
	writeAttachment(t, s, "4-x.txt", "abcdef")
	req := httptest.NewRequest("GET", "/api/attachments/read?stored=4-x.txt&offset=2&max_bytes=3", nil)
	rec := httptest.NewRecorder()
	s.handleAttachmentRead(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var seg attachmentSegment
	if err := json.Unmarshal(rec.Body.Bytes(), &seg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if seg.Content != "cde" || seg.NextOffset != 5 {
		t.Fatalf("segment: %+v", seg)
	}
}
