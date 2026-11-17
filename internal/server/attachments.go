// Chat attachments: files uploaded from the composer (+ button) are stored
// under the data dir and inlined into the message text by the client —
// text-like files come back with a preview the composer appends to the
// prompt, binary files are referenced by name only.
package server

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// maxAttachmentSize caps a single upload at 20 MB.
const maxAttachmentSize = 20 << 20

// inlinePreviewSize caps how much of a text file is inlined into a message.
const inlinePreviewSize = 64 << 10

// textExtensions is a pragmatic allowlist for inline preview; anything else
// is treated as binary and referenced by name.
var textExtensions = map[string]bool{
	".txt": true, ".md": true, ".json": true, ".yaml": true, ".yml": true,
	".toml": true, ".csv": true, ".log": true, ".go": true, ".py": true,
	".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".rs": true,
	".sh": true, ".sql": true, ".html": true, ".css": true, ".xml": true,
	".env": true, ".conf": true, ".ini": true, ".swift": true, ".kt": true,
}

// handleAttachmentUpload serves POST /api/attachments (multipart form, field
// "file"). Stores the file under <cfgDir>/attachments and returns its
// metadata plus a text preview when the type qualifies for inlining.
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
	log.Printf("server: attachment stored user=%s name=%s bytes=%d", s.auth.User(r), name, n)

	res := map[string]any{"name": name, "size": n, "stored": stored}
	// Text preview for inlining into the prompt — small text-like files only.
	if n <= inlinePreviewSize && textExtensions[strings.ToLower(filepath.Ext(name))] {
		if b, err := os.ReadFile(path); err == nil && utf8Valid(b) {
			res["text"] = string(b)
		}
	}
	writeJSON(w, http.StatusOK, res)
}

// utf8Valid reports whether b is valid UTF-8 (binary sniffing shorthand).
func utf8Valid(b []byte) bool { return utf8.Valid(b) }
