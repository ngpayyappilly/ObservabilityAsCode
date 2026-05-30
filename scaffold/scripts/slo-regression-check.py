#!/usr/bin/env python3
"""
slo-regression-check.py — Block PRs that lower SLO targets.

Compares SLO target values in the PR branch against the base branch.
Fails if any target drops by more than --threshold percent.

Skips files where the target is still a Monaco template reference
({{ .SLOTarget }}) — that means it hasn't been overridden locally.

Usage:
    python scripts/slo-regression-check.py \
        --base-branch origin/main \
        --changed-path observability/slos/ \
        --threshold 0.1
"""

from __future__ import annotations

import argparse
import subprocess
import sys
import tempfile
from pathlib import Path

import yaml


def _git_show(ref: str, path: str) -> str | None:
    """Return file content at git ref, or None if not found."""
    result = subprocess.run(
        ["git", "show", f"{ref}:{path}"],
        capture_output=True,
        text=True,
    )
    if result.returncode == 0:
        return result.stdout
    return None


def _git_changed_files(changed_path: str) -> list[str]:
    """Return list of files under changed_path that differ from HEAD."""
    result = subprocess.run(
        ["git", "diff", "--name-only", "HEAD"],
        capture_output=True,
        text=True,
    )
    files = result.stdout.splitlines()
    return [f for f in files if f.startswith(changed_path.rstrip("/"))]


def _extract_slo_targets(content: str) -> dict[str, float]:
    """
    Parse a Monaco SLO config YAML and extract target values.
    Returns {config_id: target_float}.
    Skips entries where target is a Monaco template reference.
    """
    targets: dict[str, float] = {}
    try:
        docs = list(yaml.safe_load_all(content))
    except yaml.YAMLError:
        return targets

    for doc in docs:
        if not isinstance(doc, dict):
            continue
        for config in doc.get("configs", []):
            config_id = config.get("id", "unknown")
            params = config.get("config", {}).get("parameters", {})
            slo_target = params.get("SLOTarget", {})

            if isinstance(slo_target, dict):
                val = slo_target.get("value")
            else:
                val = slo_target

            if val is None:
                continue

            val_str = str(val).strip()

            # Skip Monaco template references — not a hardcoded value
            if "{{" in val_str:
                continue

            try:
                targets[config_id] = float(val_str)
            except ValueError:
                pass

    return targets


def main() -> int:
    parser = argparse.ArgumentParser(description="Block SLO target regressions in PRs")
    parser.add_argument("--base-branch", default="origin/main",
                        help="Base branch to compare against")
    parser.add_argument("--changed-path", default="observability/slos/",
                        help="Path prefix to check for changed SLO files")
    parser.add_argument("--threshold", type=float, default=0.1,
                        help="Max allowed SLO target drop in percentage points")
    args = parser.parse_args()

    changed_files = _git_changed_files(args.changed_path)
    if not changed_files:
        print(f"No changed files under {args.changed_path} — nothing to check.")
        return 0

    print(f"Checking {len(changed_files)} SLO file(s) for target regressions...")

    regressions: list[str] = []

    for file_path in changed_files:
        base_content = _git_show(args.base_branch, file_path)
        pr_content = Path(file_path).read_text() if Path(file_path).exists() else None

        if base_content is None:
            print(f"  {file_path}: new file — skipping regression check")
            continue
        if pr_content is None:
            print(f"  {file_path}: file deleted in PR — skipping")
            continue

        base_targets = _extract_slo_targets(base_content)
        pr_targets = _extract_slo_targets(pr_content)

        for config_id, base_val in base_targets.items():
            pr_val = pr_targets.get(config_id)
            if pr_val is None:
                # Config was removed — that's a different concern
                continue
            drop = base_val - pr_val
            if drop > args.threshold:
                msg = (
                    f"  REGRESSION in {file_path}: {config_id} target dropped "
                    f"{base_val:.3f}% → {pr_val:.3f}% (drop: {drop:.3f}%, "
                    f"threshold: {args.threshold:.3f}%)"
                )
                print(msg)
                regressions.append(msg)
            else:
                print(
                    f"  OK {file_path}: {config_id} "
                    f"{base_val:.3f}% → {pr_val:.3f}%"
                )

    if regressions:
        print(
            f"\nERROR: {len(regressions)} SLO target regression(s) detected. "
            "Prod SLO targets must not decrease — they represent contractual commitments.",
            file=sys.stderr,
        )
        return 1

    print("\nOK: No SLO target regressions found.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
