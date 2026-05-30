#!/usr/bin/env python3
"""
propagate.py — Push template updates to repos that already have OaC scaffolded.

Triggered automatically when changes land in the observability-template repo.
Opens PRs only in repos where the re-rendered output differs from what's in Git.

Usage:
    python propagate.py --changed-templates /tmp/changed-templates.txt
                        --branch chore/update-oac-scaffold-<build-id>

Environment variables required:
    ADO_ORG_URL        e.g. https://dev.azure.com/YOUR_ORG
    ADO_PROJECT        ADO project name
    ADO_PAT            Personal Access Token
    PR_REVIEWER_EMAILS Comma-separated reviewer email addresses
"""

from __future__ import annotations

import argparse
import difflib
import logging
import os
import sys
from pathlib import Path

import yaml

from oac_utils import AdoClient, infer_service_name, render_templates

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
)
log = logging.getLogger("propagate")

MANIFEST_FILE = "/observability/manifest.yaml"
TEMPLATE_DIR = Path(__file__).parent.parent


def _changed_file_list(path: str) -> list[str]:
    """Read newline-separated changed template paths from a file."""
    return [
        line.strip()
        for line in Path(path).read_text().splitlines()
        if line.strip()
    ]


def _extract_slo_targets(content: str) -> dict[str, str]:
    """Parse an environments YAML and return {key: SLOTarget} for diff display."""
    try:
        data = yaml.safe_load(content) or {}
        for service_data in data.values():
            if isinstance(service_data, dict) and "SLOTarget" in service_data:
                return {"SLOTarget": str(service_data["SLOTarget"])}
    except Exception:
        pass
    return {}


def _build_file_diff(path: str, before: str, after: str) -> str:
    """Return a unified-diff string for a single file change."""
    diff = difflib.unified_diff(
        before.splitlines(keepends=True),
        after.splitlines(keepends=True),
        fromfile=f"a/{path}",
        tofile=f"b/{path}",
        n=3,
    )
    return "".join(diff)


def _build_pr_description(
    service_name: str,
    changed_files: list[str],
    diffs: dict[str, str],
) -> str:
    sections = []
    for rel_path, diff_text in diffs.items():
        sections.append(f"### `{rel_path}`\n\n```diff\n{diff_text}\n```")

    diff_body = "\n\n".join(sections) if sections else "_No content diffs available._"
    changed_list = "\n".join(f"- `{f}`" for f in sorted(changed_files))

    return f"""\
## Observability as Code — Template Update

Updated scaffold templates have been propagated to **{service_name}**.

### Changed templates

{changed_list}

### File diffs

{diff_body}

_Raised automatically by the [observability-template](https://dev.azure.com/YOUR_ORG/YOUR_PROJECT/_git/observability-template) propagation pipeline._
"""


def process_repo(
    client: AdoClient,
    repo: dict,
    changed_template_paths: list[str],
    branch: str,
    reviewer_ids: list[str],
) -> str:
    """
    Re-render changed templates and open a PR if content differs.
    Returns: 'updated' | 'skipped' | 'failed'
    """
    repo_id = repo["id"]
    repo_name = repo["name"]
    service_name = infer_service_name(repo_name)

    default_ref = client.get_default_branch_ref(repo_id)
    if not default_ref:
        return "skipped"
    default_branch = default_ref.replace("refs/heads/", "")

    # Only process repos that already have an OaC scaffold
    if not client.file_exists(repo_id, MANIFEST_FILE, default_branch):
        return "skipped"

    if client.pr_exists(repo_id, branch):
        log.info("  Skipping %s — PR already open", repo_name)
        return "skipped"

    log.info("Checking %s for template updates...", repo_name)

    try:
        all_rendered = render_templates(str(TEMPLATE_DIR), service_name)

        changed_files: list[str] = []
        files_to_push: dict[str, str] = {}
        diffs: dict[str, str] = {}

        for tmpl_path in changed_template_paths:
            # Map template path to rendered destination path
            rel = Path(tmpl_path)
            if rel.suffix == ".j2":
                dest_rel = str(rel.with_suffix(""))
            else:
                dest_rel = str(rel)

            # Strip 'scaffold/' prefix to match where file lives in the app repo
            if dest_rel.startswith("scaffold/"):
                dest_rel = dest_rel[len("scaffold/"):]

            if dest_rel not in all_rendered:
                log.debug("  Template %s has no rendered counterpart — skipping", tmpl_path)
                continue

            rendered_content = all_rendered[dest_rel]
            repo_path = f"/{dest_rel}"
            current_content = client.get_file_content(repo_id, repo_path, default_branch)

            if current_content is None:
                # File didn't exist in repo — it's new, include it
                files_to_push[repo_path] = rendered_content
                changed_files.append(dest_rel)
                diffs[dest_rel] = _build_file_diff(dest_rel, "", rendered_content)
            elif current_content.rstrip() != rendered_content.rstrip():
                files_to_push[repo_path] = rendered_content
                changed_files.append(dest_rel)
                diff_text = _build_file_diff(dest_rel, current_content, rendered_content)
                # Include SLO target values in diff description if it's an env file
                if "environments/" in dest_rel:
                    before_slo = _extract_slo_targets(current_content)
                    after_slo = _extract_slo_targets(rendered_content)
                    if before_slo != after_slo:
                        diff_text = (
                            f"SLOTarget: {before_slo.get('SLOTarget', 'n/a')} → "
                            f"{after_slo.get('SLOTarget', 'n/a')}\n\n"
                        ) + diff_text
                diffs[dest_rel] = diff_text

        if not files_to_push:
            log.info("  No changes needed for %s", repo_name)
            return "skipped"

        client.push_files(
            repo_id=repo_id,
            branch=branch,
            base_branch=default_branch,
            files=files_to_push,
            commit_message=f"chore(oac): propagate template updates to {service_name}",
        )

        client.create_pr(
            repo_id=repo_id,
            source_branch=branch,
            target_branch=default_branch,
            title=f"chore(oac): update observability scaffold for {service_name}",
            description=_build_pr_description(service_name, changed_files, diffs),
            reviewer_ids=reviewer_ids,
        )
        log.info("  PR opened for %s (%d files changed)", repo_name, len(changed_files))
        return "updated"

    except Exception as exc:
        log.error("  FAILED for %s: %s", repo_name, exc, exc_info=True)
        return "failed"


def main() -> int:
    parser = argparse.ArgumentParser(description="Propagate OaC template updates")
    parser.add_argument(
        "--changed-templates",
        required=True,
        help="Path to newline-separated file listing changed template paths",
    )
    parser.add_argument(
        "--branch",
        required=True,
        help="Branch name to push changes to (e.g. chore/update-oac-scaffold-123)",
    )
    args = parser.parse_args()

    org_url = os.environ["ADO_ORG_URL"]
    project = os.environ["ADO_PROJECT"]
    pat = os.environ["ADO_PAT"]
    reviewer_emails = [
        e.strip()
        for e in os.environ.get("PR_REVIEWER_EMAILS", "").split(",")
        if e.strip()
    ]

    client = AdoClient(org_url, project, pat)
    reviewer_ids = client.resolve_reviewer_ids(reviewer_emails) if reviewer_emails else []

    changed_templates = _changed_file_list(args.changed_templates)
    if not changed_templates:
        log.info("No changed templates — nothing to propagate")
        return 0

    log.info("Propagating %d changed template(s): %s", len(changed_templates), changed_templates)

    repos = client.list_repos()
    log.info("Found %d repos to check", len(repos))

    counts = {"updated": 0, "skipped": 0, "failed": 0}
    for repo in repos:
        outcome = process_repo(
            client=client,
            repo=repo,
            changed_template_paths=changed_templates,
            branch=args.branch,
            reviewer_ids=reviewer_ids,
        )
        counts[outcome] += 1

    log.info("Propagation complete — Updated: %d, Skipped: %d, Failed: %d",
             counts["updated"], counts["skipped"], counts["failed"])

    return 1 if counts["failed"] else 0


if __name__ == "__main__":
    sys.exit(main())
