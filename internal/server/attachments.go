// Chat attachments: files uploaded from the composer (+ button) are stored
// under the data dir; the client passes the stored absolute path to the
// model, which reads the file itself in chunks. Content is never inlined
// into the message — large documents would bloat the chat UI and the
// prompt.
package server

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"unicode/utf8"
)

// maxAttachmentSize caps a single upload at 20 MB.
const maxAttachmentSize = 20 << 20

// handleAttachmentUpload serves POST /api/attachments (multipart form, field
// "file"). Stores the file under <cfgDir>/attachments and returns its
// metadata plus the absolute stored path.
func (s *Server) handleAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentSize+1<<20)
	if err := r.ParseMultipartForm(maxAttachmentSize); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "attachment too large or bad form (max 20 MB)"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing file field"})
		return
	}
	defer file.Close()

	dir := filepath.Join(s.cfgDir, "attachments")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	name := filepath.Base(header.Filename)
	stored := fmt.Sprintf("%d-%s", time.Now().UnixNano(), name)
	path := filepath.Join(dir, stored)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	n, err := io.Copy(out, io.LimitReader(file, maxAttachmentSize))
	closeErr := out.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(path)
		serverError(w, err, http.StatusInternalServerError)
		return
	}
	log.Printf("server: attachment stored user=%s name=%s bytes=%d path=%s", s.auth.User(r), name, n, path)
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "size": n, "stored": stored, "path": path})
}

// maxReadChunk caps one segment-read response at 256 KB so a single call can
// never push an oversized document into the model context.
const maxReadChunk = 256 << 10

// attachmentSegment is one byte-range read result plus the continuation
// metadata the reader needs to walk the file (next_offset, eof).
type attachmentSegment struct {
	Stored     string `json:"stored"`
	Total      int64  `json:"total"`
	Offset     int64  `json:"offset"`
	BytesRead  int    `json:"bytes_read"`
	NextOffset int64  `json:"next_offset"` // -1 when the file is exhausted
	Encoding   string `json:"encoding"`    // text | base64
	Content    string `json:"content"`
	EOF        bool   `json:"eof"`
}

// readAttachmentSegment resolves one stored attachment (name confined to the
// attachments dir via Base) and returns the requested byte range. Shared by
// the HTTP handler and the read_attachment planner tool.
func (s *Server) readAttachmentSegment(stored string, offset, maxBytes int64) (*attachmentSegment, error) {
	stored = filepath.Base(stored)
	if stored == "." || stored == string(filepath.Separator) {
		// Base strips any directory component, so the read is confined to
		// the attachments dir; "." only comes from an empty name.
		return nil, fmt.Errorf("missing stored attachment name")
	}
	if offset < 0 {
		offset = 0
	}
	if maxBytes <= 0 {
		maxBytes = 64 << 10
	}
	if maxBytes > maxReadChunk {
		maxBytes = maxReadChunk
	}
	path := filepath.Join(s.cfgDir, "attachments", stored)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("attachment %s: %v", stored, err)
	}
	if offset >= info.Size() {
		return &attachmentSegment{Stored: stored, Total: info.Size(), Offset: offset, NextOffset: -1, Encoding: "text", EOF: true}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open attachment %s: %v", stored, err)
	}
	defer f.Close()
	buf := make([]byte, maxBytes)
	n, err := io.ReadFull(io.NewSectionReader(f, offset, maxBytes), buf)
	if err == io.ErrUnexpectedEOF || err == io.EOF {
		err = nil
	}
	if err != nil {
		return nil, fmt.Errorf("read attachment %s: %v", stored, err)
	}
	segment := buf[:n]
	encoding := "text"
	content := string(segment)
	if !utf8.Valid(segment) {
		encoding = "base64"
		content = base64.StdEncoding.EncodeToString(segment)
	}
	next := offset + int64(n)
	if next >= info.Size() {
		next = -1
	}
	return &attachmentSegment{
		Stored: stored, Total: info.Size(), Offset: offset, BytesRead: n,
		NextOffset: next, Encoding: encoding, Content: content, EOF: next < 0,
	}, nil
}

// handleAttachmentRead serves GET /api/attachments/read?stored=&offset=&max_bytes=.
// Returns one byte-range segment of a stored attachment plus the metadata the
// reader needs to continue (total size, next offset). Segmented reads keep
// large documents out of the prompt: the model fetches only the chunk it is
// working through instead of opening the whole file at once.
func (s *Server) handleAttachmentRead(w http.ResponseWriter, r *http.Request) {
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	maxBytes, _ := strconv.ParseInt(r.URL.Query().Get("max_bytes"), 10, 64)
	seg, err := s.readAttachmentSegment(r.URL.Query().Get("stored"), offset, maxBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	log.Printf("server: attachment read user=%s stored=%s offset=%d bytes=%d", s.auth.User(r), seg.Stored, seg.Offset, seg.BytesRead)
	writeJSON(w, http.StatusOK, seg)
}
