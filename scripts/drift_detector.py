#!/usr/bin/env python3
"""
drift_detector.py — Detect and optionally remediate OaC configuration drift.

Runs every 6 hours as a CronJob in sre-tools. Compares the oac/manifest-hash
annotation on live ConfigMap sentinels against what Argo CD considers the
desired state. Triggers a hard-refresh for any drifted application.

Environment variables:
    ARGOCD_SERVER    Argo CD server URL (e.g. https://argocd.internal)
    ARGOCD_TOKEN     Argo CD API token with app get/sync permissions
    SLACK_WEBHOOK    Incoming webhook URL for drift notifications
    AUTO_REMEDIATE   "true" to auto-trigger hard-refresh on drift
"""

from __future__ import annotations

import json
import logging
import os
import sys

import requests

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(name)s: %(message)s",
)
log = logging.getLogger("drift-detector")

ARGOCD_SERVER = os.environ["ARGOCD_SERVER"].rstrip("/")
ARGOCD_TOKEN = os.environ["ARGOCD_TOKEN"]
SLACK_WEBHOOK = os.environ.get("SLACK_WEBHOOK", "")
AUTO_REMEDIATE = os.environ.get("AUTO_REMEDIATE", "false").lower() == "true"

_ARGOCD_HEADERS = {
    "Authorization": f"Bearer {ARGOCD_TOKEN}",
    "Content-Type": "application/json",
}


# ---------------------------------------------------------------------------
# Argo CD API helpers
# ---------------------------------------------------------------------------

def list_oac_applications() -> list[dict]:
    """Return all Argo CD applications whose names start with 'oac-'."""
    resp = requests.get(
        f"{ARGOCD_SERVER}/api/v1/applications",
        headers=_ARGOCD_HEADERS,
        params={"selector": "app.kubernetes.io/managed-by=argocd"},
        timeout=30,
    )
    resp.raise_for_status()
    apps = resp.json().get("items", [])
    return [a for a in apps if a.get("metadata", {}).get("name", "").startswith("oac-")]


def get_argocd_desired_hash(app: dict) -> str | None:
    """
    Extract the desired oac/manifest-hash from Argo CD's cached desired state.
    Argo CD stores the rendered resource tree; we look for the ConfigMap sentinel.
    """
    name = app["metadata"]["name"]
    resp = requests.get(
        f"{ARGOCD_SERVER}/api/v1/applications/{name}/resource-tree",
        headers=_ARGOCD_HEADERS,
        timeout=30,
    )
    if resp.status_code != 200:
        log.warning("Could not get resource tree for %s: HTTP %s", name, resp.status_code)
        return None

    nodes = resp.json().get("nodes", [])
    for node in nodes:
        if node.get("kind") == "ConfigMap" and node.get("name", "").startswith("monaco-oac-state-"):
            health = node.get("health", {})
            # The hash is in the resource info fields set by Argo CD
            for info in node.get("info", []):
                if info.get("name") == "oac/manifest-hash":
                    return info.get("value")
    return None


def get_live_hash(app_name: str) -> str | None:
    """
    Read the oac/manifest-hash annotation from the live ConfigMap sentinel
    via the Argo CD managed resource API.
    """
    sentinel_name = f"monaco-oac-state-{app_name}"
    resp = requests.get(
        f"{ARGOCD_SERVER}/api/v1/applications/{app_name}/resource",
        headers=_ARGOCD_HEADERS,
        params={
            "resourceName": sentinel_name,
            "namespace": "sre-tools",
            "kind": "ConfigMap",
            "group": "",
            "version": "v1",
        },
        timeout=30,
    )
    if resp.status_code != 200:
        return None

    manifest_str = resp.json().get("manifest", "{}")
    try:
        manifest = json.loads(manifest_str)
        return manifest.get("metadata", {}).get("annotations", {}).get("oac/manifest-hash")
    except json.JSONDecodeError:
        return None


def trigger_hard_refresh(app_name: str) -> bool:
    """Trigger an Argo CD hard-refresh (force sync with hook strategy)."""
    body = {
        "strategy": {
            "hook": {
                "force": True
            }
        }
    }
    resp = requests.post(
        f"{ARGOCD_SERVER}/api/v1/applications/{app_name}/sync",
        headers=_ARGOCD_HEADERS,
        json=body,
        timeout=60,
    )
    if resp.status_code in (200, 201):
        log.info("Hard-refresh triggered for %s", app_name)
        return True
    log.error("Failed to trigger hard-refresh for %s: HTTP %s — %s",
              app_name, resp.status_code, resp.text)
    return False


# ---------------------------------------------------------------------------
# Slack notification
# ---------------------------------------------------------------------------

def post_slack_notification(
    drifted: list[dict],
    remediated: list[str],
) -> None:
    if not SLACK_WEBHOOK:
        log.info("SLACK_WEBHOOK not set — skipping notification")
        return

    if not drifted:
        return

    lines = ["*:warning: OaC Drift Detected*"]
    for item in drifted:
        emoji = ":white_check_mark:" if item["name"] in remediated else ":x:"
        lines.append(
            f"{emoji} `{item['name']}` (env: `{item['env']}`) — "
            f"live hash `{item['live_hash']}` ≠ desired `{item['desired_hash']}`"
        )

    if remediated:
        lines.append(f"\nAuto-remediated: {len(remediated)} application(s) hard-refreshed.")
    else:
        lines.append("\nAuto-remediation is disabled. Manual Argo CD sync required.")

    payload = {"text": "\n".join(lines)}
    try:
        resp = requests.post(SLACK_WEBHOOK, json=payload, timeout=10)
        resp.raise_for_status()
    except Exception as exc:
        log.error("Slack notification failed: %s", exc)


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> int:
    log.info("Starting OaC drift detection run...")
    log.info("AUTO_REMEDIATE=%s", AUTO_REMEDIATE)

    try:
        apps = list_oac_applications()
    except Exception as exc:
        log.error("Failed to list Argo CD applications: %s", exc)
        # Exit 0 — CronJob should not restart on API failure (transient)
        return 0

    log.info("Found %d OaC application(s) to check", len(apps))

    drifted: list[dict] = []
    remediated: list[str] = []

    for app in apps:
        app_name = app["metadata"]["name"]
        labels = app.get("metadata", {}).get("labels", {})
        env = labels.get("oac/environment", "unknown")

        desired_hash = get_argocd_desired_hash(app)
        live_hash = get_live_hash(app_name)

        log.debug("  %s: desired=%s live=%s", app_name, desired_hash, live_hash)

        if desired_hash is None or live_hash is None:
            log.warning("  %s: could not compare hashes (desired=%s, live=%s) — skipping",
                        app_name, desired_hash, live_hash)
            continue

        if desired_hash == live_hash:
            log.info("  %s: in sync", app_name)
            continue

        log.warning("  %s: DRIFT DETECTED (desired=%s, live=%s)", app_name, desired_hash, live_hash)
        drifted.append({
            "name": app_name,
            "env": env,
            "desired_hash": desired_hash,
            "live_hash": live_hash,
        })

        if AUTO_REMEDIATE:
            if trigger_hard_refresh(app_name):
                remediated.append(app_name)

    post_slack_notification(drifted, remediated)

    if drifted:
        log.warning(
            "Drift summary: %d drifted, %d auto-remediated",
            len(drifted),
            len(remediated),
        )
    else:
        log.info("No drift detected across %d application(s)", len(apps))

    # Always exit 0 — drift is not a crash condition for the CronJob
    return 0


if __name__ == "__main__":
    sys.exit(main())
