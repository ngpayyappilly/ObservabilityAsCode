#!/usr/bin/env python3
"""
bootstrap.py — Scaffold observability configs into every ADO repo.

Run manually or via the ADO bootstrap-pipeline.yaml.
Safe to run multiple times — idempotent by design.

Usage:
    python bootstrap.py [--dry-run] [--repo-filter REGEX]

Environment variables required:
    ADO_ORG_URL        e.g. https://dev.azure.com/YOUR_ORG
    ADO_PROJECT        ADO project name
    ADO_PAT            Personal Access Token (Code read/write, PRs)
    PR_REVIEWER_EMAILS Comma-separated reviewer email addresses
"""

from __future__ import annotations

import argparse
import json
import logging
import os
import re
import sys
from pathlib import Path

from oac_utils import AdoClient, infer_service_name, render_templates

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
)
log = logging.getLogger("bootstrap")

BRANCH_NAME = "feat/add-oac-scaffold"
TEMPLATE_DIR = Path(__file__).parent.parent  # repo root of observability-template
OPT_OUT_FILE = "/.no-oac"
MANIFEST_FILE = "/observability/manifest.yaml"


def build_pr_description(rendered_files: dict[str, str], service_name: str) -> str:
    file_list = "\n".join(f"- `{p}`" for p in sorted(rendered_files))
    return f"""\
## Observability as Code — Initial Scaffold

This PR adds Monaco v2 observability configs for **{service_name}**.

### What's included

{file_list}

### How it works

1. **Argo CD** detects `observability/manifest.yaml` via an `ApplicationSet` git generator.
2. The Monaco CMP sidecar deploys configs to Dynatrace for `dev`, `staging`, and `prod`.
3. SLO targets are per-environment in `observability/environments/`.

### Reviewing

- Check SLO targets in `observability/environments/prod.yaml` — they reflect contractual SLAs.
- Do **not** hardcode Dynatrace tenant URLs or tokens; all credentials come from Vault via ExternalSecrets.
- To opt out of OaC for this repo, create a `.no-oac` file and close this PR.

_Generated automatically by the [observability-template](https://dev.azure.com/YOUR_ORG/YOUR_PROJECT/_git/observability-template) bootstrap pipeline._
"""


def process_repo(
    client: AdoClient,
    repo: dict,
    rendered_files: dict[str, str],
    reviewer_ids: list[str],
    dry_run: bool,
) -> str:
    """
    Scaffold one repo. Returns: 'scaffolded' | 'skipped' | 'failed'.
    """
    repo_id = repo["id"]
    repo_name = repo["name"]
    service_name = infer_service_name(repo_name)

    log.info("Processing repo: %s (service: %s)", repo_name, service_name)

    # Repos with no default branch are empty — nothing to scaffold into.
    default_ref = client.get_default_branch_ref(repo_id)
    if not default_ref:
        log.warning("  Skipping %s — no default branch (empty repo)", repo_name)
        return "skipped"

    default_branch = default_ref.replace("refs/heads/", "")

    # Opt-out check
    if client.file_exists(repo_id, OPT_OUT_FILE, default_branch):
        log.info("  Skipping %s — .no-oac opt-out present", repo_name)
        return "skipped"

    # Already scaffolded check
    if client.file_exists(repo_id, MANIFEST_FILE, default_branch):
        log.info("  Skipping %s — already scaffolded", repo_name)
        return "skipped"

    # Existing PR check
    if client.pr_exists(repo_id, BRANCH_NAME):
        log.info("  Skipping %s — PR already open on branch %s", repo_name, BRANCH_NAME)
        return "skipped"

    if dry_run:
        log.info("  [DRY RUN] Would scaffold %s with %d files", repo_name, len(rendered_files))
        return "skipped"

    try:
        # Re-render templates substituting this repo's service name
        files = render_templates(str(TEMPLATE_DIR), service_name)

        # Prefix all paths with /observability/ or /scripts/ as appropriate
        prefixed: dict[str, str] = {}
        for rel_path, content in files.items():
            if rel_path.startswith("scripts/"):
                prefixed[f"/{rel_path}"] = content
            else:
                # strip leading 'observability/' — render_templates already does this
                prefixed[f"/{rel_path}"] = content

        client.push_files(
            repo_id=repo_id,
            branch=BRANCH_NAME,
            base_branch=default_branch,
            files=prefixed,
            commit_message=f"chore(oac): scaffold Monaco observability configs for {service_name}",
        )

        pr = client.create_pr(
            repo_id=repo_id,
            source_branch=BRANCH_NAME,
            target_branch=default_branch,
            title=f"chore(oac): add observability scaffold for {service_name}",
            description=build_pr_description(prefixed, service_name),
            reviewer_ids=reviewer_ids,
        )
        log.info("  PR created: %s", pr.get("url", pr.get("_links", {}).get("web", {}).get("href")))
        return "scaffolded"

    except Exception as exc:
        log.error("  FAILED for %s: %s", repo_name, exc, exc_info=True)
        return "failed"


def main() -> int:
    parser = argparse.ArgumentParser(description="Bootstrap OaC scaffold into ADO repos")
    parser.add_argument("--dry-run", action="store_true", help="List repos without writing")
    parser.add_argument("--repo-filter", default="", help="Regex to filter repo names")
    args = parser.parse_args()

    org_url = os.environ["ADO_ORG_URL"]
    project = os.environ["ADO_PROJECT"]
    pat = os.environ["ADO_PAT"]
    reviewer_emails_raw = os.environ.get("PR_REVIEWER_EMAILS", "")
    reviewer_emails = [e.strip() for e in reviewer_emails_raw.split(",") if e.strip()]

    client = AdoClient(org_url, project, pat)

    log.info("Resolving %d reviewer email(s)...", len(reviewer_emails))
    reviewer_ids = client.resolve_reviewer_ids(reviewer_emails) if reviewer_emails else []

    log.info("Listing repos in project %s...", project)
    repos = client.list_repos()
    log.info("Found %d repos", len(repos))

    if args.repo_filter:
        pattern = re.compile(args.repo_filter, re.IGNORECASE)
        repos = [r for r in repos if pattern.search(r["name"])]
        log.info("After filter '%s': %d repos", args.repo_filter, len(repos))

    results: dict[str, list[str]] = {"scaffolded": [], "skipped": [], "failed": []}

    # Pre-render templates once with a placeholder — actual render happens per-repo
    # to substitute the correct service name. This call validates templates are parseable.
    try:
        render_templates(str(TEMPLATE_DIR), "__validate__")
    except Exception as exc:
        log.error("Template validation failed: %s", exc)
        return 1

    for repo in repos:
        outcome = process_repo(
            client=client,
            repo=repo,
            rendered_files={},  # render happens inside process_repo per service
            reviewer_ids=reviewer_ids,
            dry_run=args.dry_run,
        )
        results[outcome].append(repo["name"])

    # Write results JSON
    results_path = Path("bootstrap-results.json")
    results_path.write_text(json.dumps(results, indent=2))
    log.info(
        "Done. Scaffolded: %d, Skipped: %d, Failed: %d",
        len(results["scaffolded"]),
        len(results["skipped"]),
        len(results["failed"]),
    )
    log.info("Results written to %s", results_path)

    return 1 if results["failed"] else 0


if __name__ == "__main__":
    sys.exit(main())
