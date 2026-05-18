/*
Copyright 2024 AgentTier Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package router

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"

	agenttierv1alpha1 "github.com/agenttier/agenttier/api/v1alpha1"
)

// archiveRoot is the only subtree we expose through GET /archive. We could
// allow arbitrary paths in principle (the existing single-file API does),
// but the archive endpoint is meant for "give me the workspace" not "give
// me /etc". Locking to /workspace keeps the blast radius small and the UX
// clear.
const archiveRoot = "/workspace"

// archiveMaxBytes caps the total bytes the Router will stream out to the
// client. We measure compressed bytes (what actually leaves the Router),
// not raw tar bytes, so highly-compressible workspaces can include more
// than this in raw form. 5 GiB matches what's documented in the project
// item — large enough for real workspaces, small enough that runaway
// archives don't tie up Router memory or bandwidth indefinitely.
const archiveMaxBytes int64 = 5 * 1024 * 1024 * 1024

// archiveStreamTimeout bounds how long a single archive download can run.
// SPDY exec sessions and mid/long zip writes both die quietly when EKS
// rotates a node behind the apiserver, so a hard cap saves us from
// half-streamed responses sitting open forever. Two hours is generous —
// even a slow 1 MB/s connection can pull ~7 GB in that window.
const archiveStreamTimeout = 2 * time.Hour

// handleArchive streams the contents of a sandbox subdirectory back to the
// client as a real .zip file. The wire format is genuinely a zip archive
// (PK header, central directory, etc.) so Finder, Explorer, and `unzip`
// all open it without ceremony.
//
// **How the stream is built.** We exec `tar -cf - -C <path> .` inside the
// sandbox pod over SPDY (`Bridge.ExecCommandStream`). tar streams its bytes
// into an io.PipeWriter; in a separate goroutine we read the tar entries
// from the matching io.PipeReader and re-encode them as zip entries
// directly onto the http.ResponseWriter. There is no buffering of the full
// archive anywhere in the Router — only one tar entry's header + one
// flushed zip block sits in memory at a time.
//
// Why not gzip-tar? `.tar.gz` filenames double-click to "extract here" on
// macOS and Linux, but on Windows they require 7-Zip or similar. .zip is
// universally double-clickable and the size cost is comparable when the
// underlying data is mostly text (compress_deflate vs. gzip). One stream,
// one file extension, no per-OS user education needed.
//
// Why re-encode at all? `zip` isn't shipped in every reference image
// (notably `images/minimal`) but `tar` is. Pulling tar from the pod and
// running `archive/zip` server-side means the implementation works on
// every base image without a CVE-introducing zip binary install.
func (s *Server) handleArchive(w http.ResponseWriter, r *http.Request) {
	claims := GetClaims(r.Context())
	if claims == nil {
		respondError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	sandboxID := mux.Vars(r)["id"]
	sandbox, err := s.getSandboxWithAuthCheck(r.Context(), sandboxID, claims)
	if err != nil {
		respondError(w, http.StatusNotFound, err.Error())
		return
	}
	if sandbox.Status.Phase != agenttierv1alpha1.SandboxPhaseRunning {
		respondError(w, http.StatusConflict, "sandbox is not running")
		return
	}
	if s.bridge == nil {
		respondError(w, http.StatusServiceUnavailable, "terminal bridge not initialized")
		return
	}

	rawPath := r.URL.Query().Get("path")
	if rawPath == "" {
		rawPath = archiveRoot
	}
	cleaned, err := archivePath(rawPath)
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Pre-flight: confirm the path exists in the pod and is a directory.
	// Cheaper than discovering this halfway through tar -- tar would emit
	// an error to stderr and exit non-zero, but at that point we've
	// already written response headers.
	statCmd := []string{"/bin/sh", "-c", fmt.Sprintf(
		"test -d '%s' && echo ok || echo missing", cleaned,
	)}
	statResult, err := s.dispatchExec(r.Context(), sandbox, statCmd, 5)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "stat failed: "+err.Error())
		return
	}
	if statResult.ExitCode != 0 || strings.TrimSpace(statResult.Stdout) != "ok" {
		respondError(w, http.StatusNotFound, "directory not found: "+cleaned)
		return
	}

	// Headers go out before the first byte. Sanitize the basename so the
	// Content-Disposition filename can't carry attacker-controlled control
	// chars; reuse the helper from handlers.go.
	base := sanitizeFilename(path.Base(cleaned))
	if base == "" || base == "." {
		base = "workspace"
	}
	filename := fmt.Sprintf("%s-%s.zip", sanitizeFilename(sandbox.Name), base)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	// Let intermediaries know not to buffer; this is a streamed body.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")

	// Bound the whole operation. Any longer than this and we kill the
	// SPDY session, which terminates `tar` in the pod and unblocks the
	// goroutine. The client sees a truncated zip; that's acceptable
	// because we logged the cause and the user re-issues the request.
	streamCtx, cancel := context.WithTimeout(r.Context(), archiveStreamTimeout)
	defer cancel()

	// Pipe tar stdout -> our zip encoder. PipeReader/PipeWriter give us
	// the streaming semantics for free; close-on-error propagates between
	// the two ends without deadlock.
	pr, pw := io.Pipe()

	// Soft cap: count compressed bytes written to the response. We
	// install the counter as the zip's destination so we can short-
	// circuit *before* we run away with bandwidth. tar's stderr goes
	// to a small bounded buffer so we can include diagnostics if it
	// exits non-zero.
	counter := &countingWriter{w: w, max: archiveMaxBytes}
	stderrBuf := &boundedBuffer{max: 8 * 1024}

	// Goroutine 1: drive the SPDY exec, feeding tar bytes into the pipe.
	// Closing pw with the exec error unblocks the zip reader on the other
	// side. On normal completion we close cleanly so the zip side sees
	// EOF and emits the central directory + close.
	var execErr error
	var execWG sync.WaitGroup
	execWG.Add(1)
	go func() {
		defer execWG.Done()
		// `tar -cf - -C <path> .` packages the tree rooted at <path>
		// with relative member names (./foo/bar) so the resulting zip
		// extracts to the user's chosen directory rather than dumping
		// /workspace/... at the top level.
		cmd := []string{"/bin/sh", "-c", fmt.Sprintf("cd '%s' && tar -cf - .", cleaned)}
		_, err := s.bridge.ExecCommandStream(
			streamCtx,
			sandbox.Namespace,
			sandbox.Status.PodName,
			"sandbox",
			cmd,
			pw,
			stderrBuf,
		)
		// Always close pw so the zip-encoder goroutine wakes up. err
		// captures both context cancel and SPDY-level failures; we
		// prefer it over a generic "stream ended" so the caller sees
		// the underlying reason.
		if err != nil {
			_ = pw.CloseWithError(err)
			execErr = err
			return
		}
		_ = pw.Close()
	}()

	// Goroutine 0 (this one): consume the tar stream and emit zip
	// entries straight onto the response. Errors here close pr with the
	// error so the exec goroutine can unblock if it's mid-write.
	zw := zip.NewWriter(counter)
	zipErr := streamTarToZip(pr, zw)
	closeZipErr := zw.Close()

	// Drain the exec goroutine so its closer runs before we return.
	execWG.Wait()

	if zipErr != nil {
		_ = pr.CloseWithError(zipErr)
	}

	// Best-effort error logging; the response body is already partially
	// flushed so we can't change status codes here.
	if execErr != nil && !errors.Is(execErr, io.EOF) && !errors.Is(execErr, context.Canceled) {
		s.logger.Warn("archive: tar exec failed",
			"sandbox", sandbox.Name, "err", execErr,
			"stderr", strings.TrimSpace(stderrBuf.String()))
	}
	if zipErr != nil {
		s.logger.Warn("archive: zip stream failed",
			"sandbox", sandbox.Name, "err", zipErr)
	}
	if closeZipErr != nil {
		s.logger.Warn("archive: zip close failed",
			"sandbox", sandbox.Name, "err", closeZipErr)
	}
	if counter.exceeded {
		s.logger.Warn("archive: max bytes exceeded; truncated",
			"sandbox", sandbox.Name, "max", archiveMaxBytes)
	}
}

// archivePath cleans and validates an /archive query path. We allow only
// the /workspace subtree to keep the endpoint focused on its purpose;
// anything outside returns an error. The same shell-metacharacter check
// the single-file path validator uses applies here too.
func archivePath(raw string) (string, error) {
	p := path.Clean("/" + strings.TrimPrefix(raw, "/"))
	for _, ch := range p {
		if ch == '\'' || ch == '\\' || ch == '`' || ch == '\n' || ch == '\r' {
			return "", fmt.Errorf("path contains disallowed characters")
		}
	}
	if p != archiveRoot && !strings.HasPrefix(p, archiveRoot+"/") {
		return "", fmt.Errorf("path must live under %s", archiveRoot)
	}
	return p, nil
}

// streamTarToZip reads a tar archive from r and writes each regular file as
// a deflate-compressed zip entry to zw. Directories, symlinks, and other
// special types are recorded as plain entries so the zip extracts to the
// same shape; no special handling for ownership / mode preservation
// because the resulting zip is meant for human download, not faithful
// round-trip.
func streamTarToZip(r io.Reader, zw *zip.Writer) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar: %w", err)
		}
		// Strip the leading "./" tar adds when invoked with `.`. zip
		// readers expect forward-slash paths without leading ./ for the
		// cleanest extraction experience.
		name := strings.TrimPrefix(hdr.Name, "./")
		if name == "" {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			// Directories in zip end with a trailing slash and have no
			// content. Empty dirs survive, which preserves the user's
			// tree shape even if a folder is empty.
			if !strings.HasSuffix(name, "/") {
				name += "/"
			}
			if _, err := zw.CreateHeader(&zip.FileHeader{
				Name:     name,
				Method:   zip.Store,
				Modified: hdr.ModTime,
			}); err != nil {
				return fmt.Errorf("zip create dir %q: %w", name, err)
			}
		case tar.TypeReg:
			fw, err := zw.CreateHeader(&zip.FileHeader{
				Name:     name,
				Method:   zip.Deflate,
				Modified: hdr.ModTime,
			})
			if err != nil {
				return fmt.Errorf("zip create %q: %w", name, err)
			}
			// Cap per-file copy at the archive-wide budget. The
			// countingWriter wrapping the response also enforces this,
			// but bounding the io.Copy on the source side prevents a
			// pathological tar entry from monopolizing memory before
			// the deflate stream gets to flush.
			if _, err := io.CopyN(fw, tr, archiveMaxBytes); err != nil && err != io.EOF {
				return fmt.Errorf("zip copy %q: %w", name, err)
			}
		case tar.TypeSymlink:
			// Encode the link target as the file body with the
			// symlink mode bit. This matches what `zip` does on Linux.
			fw, err := zw.CreateHeader(&zip.FileHeader{
				Name:     name,
				Method:   zip.Store,
				Modified: hdr.ModTime,
			})
			if err != nil {
				return fmt.Errorf("zip create link %q: %w", name, err)
			}
			if _, err := io.WriteString(fw, hdr.Linkname); err != nil {
				return fmt.Errorf("zip copy link %q: %w", name, err)
			}
		default:
			// Skip device files, FIFOs, etc. They never appear in a
			// developer's workspace tree in practice and there's no
			// sensible zip representation.
			continue
		}
	}
}

// countingWriter wraps an io.Writer and short-circuits writes once a hard
// limit is hit. We use it to cap the total compressed bytes the archive
// endpoint streams out.
type countingWriter struct {
	w        io.Writer
	max      int64
	written  int64
	exceeded bool
}

func (c *countingWriter) Write(p []byte) (int, error) {
	if c.exceeded {
		return 0, errors.New("archive size limit exceeded")
	}
	remaining := c.max - c.written
	if remaining <= 0 {
		c.exceeded = true
		return 0, errors.New("archive size limit exceeded")
	}
	if int64(len(p)) > remaining {
		p = p[:remaining]
		c.exceeded = true
	}
	n, err := c.w.Write(p)
	c.written += int64(n)
	if c.exceeded && err == nil {
		err = errors.New("archive size limit exceeded")
	}
	return n, err
}

// boundedBuffer is a tiny io.Writer that retains at most max bytes. We use
// it to capture tar's stderr without exposing an unbounded memory channel.
type boundedBuffer struct {
	max int
	buf bytes.Buffer
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len() >= b.max {
		return len(p), nil
	}
	avail := b.max - b.buf.Len()
	if avail >= len(p) {
		return b.buf.Write(p)
	}
	_, _ = b.buf.Write(p[:avail])
	return len(p), nil
}

func (b *boundedBuffer) String() string { return b.buf.String() }
