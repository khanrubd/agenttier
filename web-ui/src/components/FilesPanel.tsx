/*
 * Copyright 2024 AgentTier Authors.
 * SPDX-License-Identifier: Apache-2.0
 */

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  listFiles,
  uploadFile,
  downloadFileUrl,
  archiveUrl,
  type FileEntry,
} from '../api/client';

interface Props {
  sandboxId: string;
  /** Panel only renders for Running sandboxes — the exec bridge backing the file API needs a live pod. */
  running: boolean;
}

const ROOT = '/workspace';
const MAX_UPLOAD_BYTES = 32 * 1024 * 1024; // matches the Router-side cap

function prettySize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
  return `${(bytes / 1024 / 1024 / 1024).toFixed(1)} GB`;
}

// joinPath safely concatenates a parent path and a child name. Strips
// duplicate slashes so the breadcrumb never produces "/workspace//foo".
function joinPath(parent: string, name: string): string {
  const cleaned = parent.replace(/\/+$/, '');
  return `${cleaned}/${name}`.replace(/\/+/g, '/');
}

// parentOf returns the parent directory of `p`. Stops at ROOT — we never
// navigate above /workspace from this panel.
function parentOf(p: string): string {
  if (p === ROOT || !p.startsWith(ROOT)) return ROOT;
  const idx = p.lastIndexOf('/');
  if (idx <= ROOT.length - 1) return ROOT;
  const parent = p.slice(0, idx);
  return parent.length < ROOT.length ? ROOT : parent;
}

// breadcrumbSegments turns "/workspace/src/api" into clickable segments:
// [{ label: "workspace", path: "/workspace" }, { label: "src", path: "/workspace/src" }, ...]
function breadcrumbSegments(p: string): { label: string; path: string }[] {
  const tail = p.startsWith(ROOT) ? p.slice(ROOT.length) : '';
  const parts = tail.split('/').filter(Boolean);
  const segs = [{ label: 'workspace', path: ROOT }];
  let acc = ROOT;
  for (const part of parts) {
    acc = `${acc}/${part}`;
    segs.push({ label: part, path: acc });
  }
  return segs;
}

export default function FilesPanel({ sandboxId, running }: Props) {
  const [entries, setEntries] = useState<FileEntry[]>([]);
  const [path, setPath] = useState(ROOT);
  const [loading, setLoading] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const reload = useCallback(async () => {
    if (!running) return;
    setLoading(true);
    setError(null);
    try {
      const res = await listFiles(sandboxId, path);
      // Sort entries: directories first, then files, both alphabetical.
      // Predictable ordering matches what users expect from a file browser.
      const sorted = [...res.entries].sort((a, b) => {
        if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
        return a.name.localeCompare(b.name);
      });
      setEntries(sorted);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : 'Failed to list files');
    } finally {
      setLoading(false);
    }
  }, [sandboxId, path, running]);

  useEffect(() => {
    if (running) reload();
  }, [reload, running]);

  const segments = useMemo(() => breadcrumbSegments(path), [path]);
  const atRoot = path === ROOT;

  if (!running) return null;

  const onPickFile = () => fileInputRef.current?.click();

  const onFileChosen: React.ChangeEventHandler<HTMLInputElement> = async (e) => {
    const file = e.target.files?.[0];
    // Clear the input so picking the same file twice fires change.
    if (fileInputRef.current) fileInputRef.current.value = '';
    if (!file) return;
    setError(null);
    setInfo(null);
    if (file.size > MAX_UPLOAD_BYTES) {
      setError(`File is ${prettySize(file.size)} — max ${prettySize(MAX_UPLOAD_BYTES)} per upload.`);
      return;
    }
    setUploading(true);
    try {
      await uploadFile(sandboxId, path, file);
      setInfo(`Uploaded ${file.name} (${prettySize(file.size)})`);
      await reload();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Upload failed');
    } finally {
      setUploading(false);
    }
  };

  return (
    <div
      data-testid="files-panel"
      style={{
        marginTop: '12px',
        padding: '10px 12px',
        borderRadius: '8px',
        background: '#f9fafc',
        border: '1px dashed #d0d4e0',
      }}
    >
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          marginBottom: '6px',
          gap: '8px',
        }}
      >
        <span
          style={{
            fontSize: '12px',
            fontWeight: 600,
            color: '#4b4657',
            textTransform: 'uppercase',
            letterSpacing: '0.04em',
          }}
        >
          Files
        </span>
        <a
          data-testid="files-archive-workspace"
          href={archiveUrl(sandboxId, ROOT)}
          download
          style={{
            fontSize: '12px',
            padding: '4px 12px',
            borderRadius: '4px',
            background: '#aa3bff',
            color: '#fff',
            textDecoration: 'none',
            fontWeight: 500,
          }}
          title="Stream the entire /workspace tree as a .zip download"
        >
          Download workspace as zip
        </a>
      </div>

      {/* Breadcrumb. Each segment is clickable except the last (current dir). */}
      <div
        data-testid="files-breadcrumb"
        style={{
          display: 'flex',
          alignItems: 'center',
          gap: '4px',
          flexWrap: 'wrap',
          fontSize: '12px',
          color: '#6b6375',
          marginBottom: '8px',
        }}
      >
        <button
          data-testid="files-up"
          type="button"
          onClick={() => setPath(parentOf(path))}
          disabled={atRoot}
          style={{
            padding: '2px 8px',
            fontSize: '11px',
            borderRadius: '4px',
            border: '1px solid #d4d0e0',
            background: atRoot ? '#f3eef9' : '#fff',
            color: atRoot ? '#a89dbb' : '#4b4657',
            cursor: atRoot ? 'not-allowed' : 'pointer',
          }}
          title={atRoot ? 'Already at /workspace' : 'Go up one directory'}
        >
          ↑ Up
        </button>
        {segments.map((seg, i) => {
          const isLast = i === segments.length - 1;
          return (
            <span key={seg.path} style={{ display: 'inline-flex', alignItems: 'center', gap: '4px' }}>
              {i > 0 && <span style={{ color: '#a89dbb' }}>/</span>}
              {isLast ? (
                <code data-testid="files-breadcrumb-current" style={{ color: '#08060d', fontWeight: 500 }}>
                  {seg.label}
                </code>
              ) : (
                <button
                  data-testid="files-breadcrumb-segment"
                  type="button"
                  onClick={() => setPath(seg.path)}
                  style={{
                    background: 'none',
                    border: 'none',
                    padding: 0,
                    color: '#6d28d9',
                    cursor: 'pointer',
                    textDecoration: 'underline',
                    fontSize: '12px',
                  }}
                >
                  {seg.label}
                </button>
              )}
            </span>
          );
        })}
        {loading && <span style={{ marginLeft: '8px', fontSize: '11px' }}>loading…</span>}
      </div>

      {entries.length === 0 && !loading && !error && (
        <div data-testid="files-empty" style={{ fontSize: '12px', color: '#6b6375', marginBottom: '8px' }}>
          No files in {path} yet.
        </div>
      )}

      {entries.length > 0 && (
        <ul
          data-testid="files-list"
          style={{ margin: 0, padding: 0, listStyle: 'none', marginBottom: '8px', maxHeight: '320px', overflowY: 'auto' }}
        >
          {entries.map((entry) => {
            const full = joinPath(path, entry.name);
            return (
              <li
                key={entry.name}
                data-testid="file-entry"
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '8px',
                  padding: '4px 0',
                  fontSize: '13px',
                  borderBottom: '1px solid #f0edf2',
                }}
              >
                <span style={{ fontSize: '14px', width: '16px', textAlign: 'center' }}>
                  {entry.isDir ? '📁' : '📄'}
                </span>
                {entry.isDir ? (
                  <button
                    data-testid="file-folder-open"
                    type="button"
                    onClick={() => setPath(full)}
                    style={{
                      flex: 1,
                      textAlign: 'left',
                      background: 'none',
                      border: 'none',
                      padding: 0,
                      color: '#6d28d9',
                      fontWeight: 500,
                      cursor: 'pointer',
                      textDecoration: 'underline',
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {entry.name}
                  </button>
                ) : (
                  <span
                    data-testid="file-name"
                    style={{
                      flex: 1,
                      color: '#08060d',
                      overflow: 'hidden',
                      textOverflow: 'ellipsis',
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {entry.name}
                  </span>
                )}
                {!entry.isDir && (
                  <span style={{ fontSize: '11px', color: '#6b6375', minWidth: '60px', textAlign: 'right' }}>
                    {prettySize(entry.size)}
                  </span>
                )}
                {entry.isDir ? (
                  <a
                    data-testid="file-folder-archive"
                    href={archiveUrl(sandboxId, full)}
                    download
                    style={{
                      fontSize: '11px',
                      color: '#aa3bff',
                      textDecoration: 'none',
                      padding: '1px 8px',
                      borderRadius: '4px',
                      border: '1px solid #d4d0e0',
                    }}
                    title={`Download ${entry.name} as a zip`}
                  >
                    download zip
                  </a>
                ) : (
                  <a
                    data-testid="file-download"
                    href={downloadFileUrl(sandboxId, full)}
                    download={entry.name}
                    style={{
                      fontSize: '11px',
                      color: '#aa3bff',
                      textDecoration: 'none',
                      padding: '1px 8px',
                      borderRadius: '4px',
                      border: '1px solid #d4d0e0',
                    }}
                  >
                    download
                  </a>
                )}
              </li>
            );
          })}
        </ul>
      )}

      <div style={{ display: 'flex', gap: '6px', alignItems: 'center' }}>
        <button
          data-testid="files-upload-button"
          type="button"
          onClick={onPickFile}
          disabled={uploading}
          style={{
            padding: '4px 12px',
            fontSize: '12px',
            borderRadius: '4px',
            border: 'none',
            background: uploading ? '#d1d5db' : '#aa3bff',
            color: '#fff',
            fontWeight: 500,
            cursor: uploading ? 'not-allowed' : 'pointer',
          }}
        >
          {uploading ? 'Uploading…' : 'Upload file'}
        </button>
        <button
          data-testid="files-refresh-button"
          type="button"
          onClick={reload}
          disabled={loading || uploading}
          style={{
            padding: '4px 10px',
            fontSize: '12px',
            borderRadius: '4px',
            border: '1px solid #d4d0e0',
            background: '#fff',
            color: '#4b4657',
            cursor: loading || uploading ? 'not-allowed' : 'pointer',
          }}
        >
          Refresh
        </button>
        <span style={{ marginLeft: 'auto', fontSize: '11px', color: '#6b6375' }}>
          max {prettySize(MAX_UPLOAD_BYTES)} per upload
        </span>
        <input
          ref={fileInputRef}
          data-testid="files-upload-input"
          type="file"
          style={{ display: 'none' }}
          onChange={onFileChosen}
        />
      </div>

      {error && (
        <div data-testid="files-error" style={{ marginTop: '6px', fontSize: '12px', color: '#dc2626' }}>
          {error}
        </div>
      )}
      {info && !error && (
        <div data-testid="files-info" style={{ marginTop: '6px', fontSize: '12px', color: '#059669' }}>
          {info}
        </div>
      )}
    </div>
  );
}
