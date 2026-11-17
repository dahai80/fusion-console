// Chat attachments: files uploaded from the composer (+ button) are stored
// under the data dir; the client passes the stored absolute path to the
// model, which reads the file itself in chunks. Content is never inlined
// into the message — large documents would bloat the chat UI and the
// prompt.
package server

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
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
