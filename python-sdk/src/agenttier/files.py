# Copyright 2024 AgentTier Authors.
# SPDX-License-Identifier: Apache-2.0

"""File operations API for sandboxes."""

from __future__ import annotations

from pathlib import PurePosixPath
from typing import Optional

import httpx

from agenttier.models import FileInfo


class FilesAPI:
    """Read, write, and manage files inside a sandbox.

    Usage:
        # Write a file
        sandbox.files.write("/workspace/hello.txt", b"Hello, world!")

        # Read a file
        content = sandbox.files.read("/workspace/hello.txt")

        # List directory
        files = sandbox.files.list("/workspace")

        # Upload from local disk
        sandbox.files.upload("./local_file.py", "/workspace/remote_file.py")

        # Download to local disk
        sandbox.files.download("/workspace/output.csv", "./output.csv")
    """

    def __init__(self, http: httpx.Client, sandbox_id: str) -> None:
        self._http = http
        self._sandbox_id = sandbox_id

    def read(self, path: str) -> bytes:
        """Read a file from the sandbox.

        Args:
            path: Absolute path in the sandbox filesystem.

        Returns:
            File contents as bytes.

        Raises:
            httpx.HTTPStatusError: If the file is not found (404) or access denied (403).
        """
        clean_path = path.lstrip("/")
        resp = self._http.get(f"/sandboxes/{self._sandbox_id}/files/{clean_path}")
        resp.raise_for_status()
        return resp.content

    def write(self, path: str, content: bytes | str) -> None:
        """Write a file to the sandbox.

        Args:
            path: Absolute path in the sandbox filesystem.
            content: File contents (bytes or string).

        Raises:
            httpx.HTTPStatusError: If the path is restricted (403) or sandbox not running.
        """
        if isinstance(content, str):
            content = content.encode("utf-8")

        clean_path = path.lstrip("/")
        resp = self._http.put(
            f"/sandboxes/{self._sandbox_id}/files/{clean_path}",
            content=content,
            headers={"Content-Type": "application/octet-stream"},
        )
        resp.raise_for_status()

    def list(self, path: str = "/workspace") -> list[FileInfo]:
        """List files in a directory.

        Args:
            path: Directory path to list.

        Returns:
            List of FileInfo objects.
        """
        resp = self._http.get(
            f"/sandboxes/{self._sandbox_id}/files/",
            params={"path": path},
        )
        resp.raise_for_status()
        data = resp.json()

        return [FileInfo(**f) for f in data.get("files", [])]

    def upload(self, local_path: str, remote_path: str) -> None:
        """Upload a local file to the sandbox.

        Args:
            local_path: Path to the local file.
            remote_path: Destination path in the sandbox.
        """
        content = open(local_path, "rb").read()
        self.write(remote_path, content)

    def download(self, remote_path: str, local_path: str) -> None:
        """Download a file from the sandbox to local disk.

        Args:
            remote_path: Path in the sandbox filesystem.
            local_path: Local destination path.
        """
        content = self.read(remote_path)
        with open(local_path, "wb") as f:
            f.write(content)

    def exists(self, path: str) -> bool:
        """Check if a file exists in the sandbox.

        Args:
            path: Path to check.

        Returns:
            True if the file exists.
        """
        try:
            clean_path = path.lstrip("/")
            resp = self._http.head(f"/sandboxes/{self._sandbox_id}/files/{clean_path}")
            return resp.status_code == 200
        except httpx.HTTPStatusError:
            return False
