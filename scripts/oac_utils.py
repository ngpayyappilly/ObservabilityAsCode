"""
oac_utils.py — Shared utilities for bootstrap.py and propagate.py.

All ADO REST calls use api-version=7.1.
PAT authentication: Basic base64(:{pat}) — note the leading colon.
"""

from __future__ import annotations

import base64
import json
import logging
import re
import time
from pathlib import Path
from typing import Optional

import requests
from jinja2 import Environment, FileSystemLoader, StrictUndefined

log = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# ADO REST client
# ---------------------------------------------------------------------------

_ADO_API_VERSION = "7.1"
_RETRY_STATUS_CODES = {429, 500, 502, 503, 504}
_MAX_RETRIES = 5
_BACKOFF_BASE = 1.0  # seconds


def _pat_header(pat: str) -> dict[str, str]:
    """Encode PAT following ADO convention: Basic base64(:{pat})."""
    token = base64.b64encode(f":{pat}".encode()).decode()
    return {"Authorization": f"Basic {token}"}


class AdoClient:
    """Thin wrapper around the ADO REST API with retry/backoff logic."""

    def __init__(self, org_url: str, project: str, pat: str) -> None:
        """
        Args:
            org_url:  e.g. https://dev.azure.com/YOUR_ORG
            project:  ADO project name
            pat:      Personal Access Token
        """
        self.org_url = org_url.rstrip("/")
        self.project = project
        self.pat = pat
        self._headers = {
            **_pat_header(pat),
            "Content-Type": "application/json",
            "Accept": "application/json",
        }

    # ------------------------------------------------------------------
    # Internal helpers
    # ------------------------------------------------------------------

    def _request(
        self,
        method: str,
        url: str,
        **kwargs,
    ) -> requests.Response:
        """Execute an HTTP request with exponential backoff on transient errors."""
        for attempt in range(_MAX_RETRIES):
            resp = requests.request(method, url, headers=self._headers, **kwargs)
            if resp.status_code not in _RETRY_STATUS_CODES:
                return resp
            wait = _BACKOFF_BASE * (2**attempt)
            retry_after = resp.headers.get("Retry-After")
            if retry_after:
                wait = max(wait, float(retry_after))
            log.warning(
                "HTTP %s from %s (attempt %d/%d); retrying in %.1fs",
                resp.status_code,
                url,
                attempt + 1,
                _MAX_RETRIES,
                wait,
            )
            time.sleep(wait)
        resp.raise_for_status()
        return resp

    def _git_url(self, repo_id: str) -> str:
        return (
            f"{self.org_url}/{self.project}/_apis/git/repositories/{repo_id}"
        )

    # ------------------------------------------------------------------
    # Repository discovery
    # ------------------------------------------------------------------

    def list_repos(self) -> list[dict]:
        """Return all repos in the configured ADO project."""
        url = (
            f"{self.org_url}/{self.project}/_apis/git/repositories"
            f"?api-version={_ADO_API_VERSION}"
        )
        resp = self._request("GET", url)
        resp.raise_for_status()
        return resp.json().get("value", [])

    # ------------------------------------------------------------------
    # File operations
    # ------------------------------------------------------------------

    def file_exists(self, repo_id: str, path: str, branch: str = "main") -> bool:
        """Return True if *path* exists on *branch* in *repo_id*."""
        url = (
            f"{self._git_url(repo_id)}/items"
            f"?path={path}&versionDescriptor.version={branch}"
            f"&api-version={_ADO_API_VERSION}"
        )
        resp = self._request("GET", url)
        return resp.status_code == 200

    def get_file_content(
        self, repo_id: str, path: str, branch: str = "main"
    ) -> Optional[str]:
        """Return raw file content or None if the file does not exist."""
        url = (
            f"{self._git_url(repo_id)}/items"
            f"?path={path}&versionDescriptor.version={branch}"
            f"&$format=text&api-version={_ADO_API_VERSION}"
        )
        resp = self._request("GET", url)
        if resp.status_code == 200:
            return resp.text
        return None

    # ------------------------------------------------------------------
    # Branch operations
    # ------------------------------------------------------------------

    def get_default_branch_ref(self, repo_id: str) -> Optional[str]:
        """
        Return the default branch ref name (e.g. 'refs/heads/main').
        Returns None if the repo has no commits yet.
        """
        url = (
            f"{self._git_url(repo_id)}"
            f"?api-version={_ADO_API_VERSION}"
        )
        resp = self._request("GET", url)
        resp.raise_for_status()
        data = resp.json()
        return data.get("defaultBranch")  # None for empty repos

    def get_branch_object_id(
        self, repo_id: str, branch: str
    ) -> Optional[str]:
        """Return the HEAD commit objectId for *branch*, or None if missing."""
        url = (
            f"{self._git_url(repo_id)}/refs"
            f"?filter=heads/{branch}&api-version={_ADO_API_VERSION}"
        )
        resp = self._request("GET", url)
        resp.raise_for_status()
        refs = resp.json().get("value", [])
        if refs:
            return refs[0]["objectId"]
        return None

    def branch_exists(self, repo_id: str, branch: str) -> bool:
        return self.get_branch_object_id(repo_id, branch) is not None

    # ------------------------------------------------------------------
    # Pushing files
    # ------------------------------------------------------------------

    def push_files(
        self,
        repo_id: str,
        branch: str,
        base_branch: str,
        files: dict[str, str],
        commit_message: str,
    ) -> None:
        """
        Push *files* (path → content) in a single commit to *branch*.
        Creates *branch* from *base_branch* if it does not yet exist.
        """
        base_oid = self.get_branch_object_id(repo_id, base_branch)
        if base_oid is None:
            raise RuntimeError(
                f"Base branch '{base_branch}' not found in repo {repo_id}"
            )

        # Determine old object ID for the target branch (0-string if new)
        old_oid = self.get_branch_object_id(repo_id, branch)
        ref_old = old_oid if old_oid else "0000000000000000000000000000000000000000"

        changes = [
            {
                "changeType": "add",
                "item": {"path": path},
                "newContent": {
                    "content": content,
                    "contentType": "rawtext",
                },
            }
            for path, content in files.items()
        ]

        body = {
            "refUpdates": [
                {
                    "name": f"refs/heads/{branch}",
                    "oldObjectId": ref_old,
                }
            ],
            "commits": [
                {
                    "comment": commit_message,
                    "changes": changes,
                }
            ],
        }

        url = (
            f"{self._git_url(repo_id)}/pushes"
            f"?api-version={_ADO_API_VERSION}"
        )
        resp = self._request("POST", url, json=body)
        if resp.status_code not in (200, 201):
            raise RuntimeError(
                f"push_files failed ({resp.status_code}): {resp.text}"
            )

    # ------------------------------------------------------------------
    # Pull request operations
    # ------------------------------------------------------------------

    def pr_exists(self, repo_id: str, source_branch: str) -> bool:
        """Return True if an open PR already exists from *source_branch*."""
        url = (
            f"{self._git_url(repo_id)}/pullrequests"
            f"?searchCriteria.status=active"
            f"&searchCriteria.sourceRefName=refs/heads/{source_branch}"
            f"&api-version={_ADO_API_VERSION}"
        )
        resp = self._request("GET", url)
        resp.raise_for_status()
        return resp.json().get("count", 0) > 0

    def create_pr(
        self,
        repo_id: str,
        source_branch: str,
        target_branch: str,
        title: str,
        description: str,
        reviewer_ids: list[str],
    ) -> dict:
        """Open a pull request and return the API response dict."""
        body: dict = {
            "title": title,
            "description": description,
            "sourceRefName": f"refs/heads/{source_branch}",
            "targetRefName": f"refs/heads/{target_branch}",
            "reviewers": [{"id": rid} for rid in reviewer_ids],
        }
        url = (
            f"{self._git_url(repo_id)}/pullrequests"
            f"?api-version={_ADO_API_VERSION}"
        )
        resp = self._request("POST", url, json=body)
        if resp.status_code not in (200, 201):
            raise RuntimeError(
                f"create_pr failed ({resp.status_code}): {resp.text}"
            )
        return resp.json()

    def resolve_reviewer_ids(self, emails: list[str]) -> list[str]:
        """
        Resolve a list of email addresses to ADO identity descriptor IDs.
        Returns only IDs that resolved successfully; logs warnings for misses.
        """
        ids: list[str] = []
        for email in emails:
            url = (
                f"{self.org_url}/_apis/identities"
                f"?searchFilter=MailAddress&filterValue={email}"
                f"&api-version={_ADO_API_VERSION}"
            )
            resp = self._request("GET", url)
            if resp.status_code != 200:
                log.warning("Could not resolve reviewer email %s: HTTP %s", email, resp.status_code)
                continue
            identities = resp.json().get("value", [])
            if identities:
                ids.append(identities[0]["id"])
            else:
                log.warning("No ADO identity found for email: %s", email)
        return ids


# ---------------------------------------------------------------------------
# Jinja2 template rendering
# ---------------------------------------------------------------------------

def render_templates(
    template_dir: str | Path,
    service_name: str,
) -> dict[str, str]:
    """
    Render every *.j2 file under *template_dir* relative to
    *template_dir*/scaffold/observability/ and return a dict mapping
    destination path → rendered content.

    The destination path strips the leading 'scaffold/' prefix so the
    rendered files land at observability/... in the target repo.
    """
    template_dir = Path(template_dir)
    scaffold_root = template_dir / "scaffold"

    env = Environment(
        loader=FileSystemLoader(str(scaffold_root)),
        undefined=StrictUndefined,
        keep_trailing_newline=True,
    )

    result: dict[str, str] = {}
    for j2_path in scaffold_root.rglob("*.j2"):
        relative = j2_path.relative_to(scaffold_root)
        template_name = str(relative)
        dest_path = str(relative)[: -len(".j2")]  # strip .j2 extension

        template = env.get_template(template_name)
        rendered = template.render(service_name=service_name)
        result[dest_path] = rendered

    # Also copy non-j2 files from scaffold/scripts/ verbatim
    scripts_src = scaffold_root / "scripts"
    if scripts_src.exists():
        for src in scripts_src.iterdir():
            if not src.suffix == ".j2":
                dest = f"scripts/{src.name}"
                result[dest] = src.read_text()

    return result


# ---------------------------------------------------------------------------
# Service name inference
# ---------------------------------------------------------------------------

def infer_service_name(repo_name: str) -> str:
    """
    Derive a DNS-label-safe service name from an ADO repo name.

    Rules (applied in order):
    1. Lowercase everything.
    2. Strip an org prefix at the first '.' (e.g. 'acme.payments-api' → 'payments-api').
    3. Replace underscores and spaces with hyphens.
    4. Strip any character that is not alphanumeric or '-'.
    5. Collapse consecutive hyphens.
    6. Strip leading/trailing hyphens.
    """
    name = repo_name.lower()
    if "." in name:
        name = name.split(".", 1)[1]
    name = name.replace("_", "-").replace(" ", "-")
    name = re.sub(r"[^a-z0-9-]", "", name)
    name = re.sub(r"-{2,}", "-", name)
    name = name.strip("-")
    return name
