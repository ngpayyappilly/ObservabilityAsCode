#!/usr/bin/env python3
"""
generate-endpoint-slos.py — Generate per-endpoint Monaco SLO configs.

Reads critical-endpoints.yaml, emits one availability + one latency SLO
config block per endpoint into observability/slos/endpoints/generated/.

Run this after editing critical-endpoints.yaml, then commit the generated/
directory. Argo CD picks it up automatically on next sync.

Usage:
    python scripts/generate-endpoint-slos.py \
        --endpoints observability/slos/endpoints/critical-endpoints.yaml \
        --env-file observability/environments/prod.yaml \
        --output-dir observability/configs/slos/endpoints/generated

The generated YAML files are standard Monaco config descriptors that
reference the JSON template files in the same directory.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path

import yaml

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def _safe_id(method: str, path: str) -> str:
    """Convert 'GET /api/v1/orders/{id}' → 'get-api-v1-orders-id'."""
    combined = f"{method.lower()}-{path}"
    slug = re.sub(r"[^a-z0-9]+", "-", combined)
    return slug.strip("-")


def _load_env_defaults(env_file: Path) -> dict:
    """Read the Monaco environment YAML and return the first service block."""
    if not env_file.exists():
        return {}
    data = yaml.safe_load(env_file.read_text()) or {}
    # env files have one top-level key = service name
    for v in data.values():
        if isinstance(v, dict):
            return v
    return {}


def _build_availability_config(
    service: str,
    endpoint: dict,
    defaults: dict,
) -> dict:
    """Return a Monaco config descriptor dict for the availability SLO."""
    ep_id = endpoint.get("id") or _safe_id(endpoint["method"], endpoint["path"])
    slo_target = endpoint.get("slo_target") or defaults.get("SLOTarget", "99.9")
    slo_warning = endpoint.get("slo_warning") or defaults.get("SLOWarning", "99.5")

    config_id = f"{service}-slo-endpoint-{ep_id}-availability"

    return {
        "id": config_id,
        "type": {"api": "slo"},
        "config": {
            "name": f"{service} {endpoint['method']} {endpoint['path']} Availability SLO",
            "template": "../endpoint-availability-slo.json",
            "parameters": {
                "ServiceName":        {"type": "value", "value": service},
                "HttpMethod":         {"type": "value", "value": endpoint["method"].upper()},
                "EndpointPath":       {"type": "value", "value": endpoint["path"]},
                "EndpointDescription":{"type": "value", "value": endpoint.get("description", endpoint["path"])},
                "SLOTarget":          {"type": "value", "value": str(slo_target)},
                "SLOWarning":         {"type": "value", "value": str(slo_warning)},
                "ManagementZone":     {"type": "environment", "name": "ManagementZone"},
            },
            "skip": False,
        },
    }


def _build_latency_config(
    service: str,
    endpoint: dict,
    defaults: dict,
) -> dict:
    """Return a Monaco config descriptor dict for the latency p99 SLO."""
    ep_id = endpoint.get("id") or _safe_id(endpoint["method"], endpoint["path"])
    latency_ms = endpoint.get("latency_ms") or defaults.get("LatencyThresholdMs", "500")
    slo_target = endpoint.get("slo_target") or defaults.get("SLOTarget", "99.9")
    slo_warning = endpoint.get("slo_warning") or defaults.get("SLOWarning", "99.5")

    config_id = f"{service}-slo-endpoint-{ep_id}-latency-p99"

    return {
        "id": config_id,
        "type": {"api": "slo"},
        "config": {
            "name": f"{service} {endpoint['method']} {endpoint['path']} Latency p99 SLO",
            "template": "../endpoint-latency-slo.json",
            "parameters": {
                "ServiceName":        {"type": "value", "value": service},
                "HttpMethod":         {"type": "value", "value": endpoint["method"].upper()},
                "EndpointPath":       {"type": "value", "value": endpoint["path"]},
                "EndpointDescription":{"type": "value", "value": endpoint.get("description", endpoint["path"])},
                "LatencyThresholdMs": {"type": "value", "value": str(latency_ms)},
                "SLOTarget":          {"type": "value", "value": str(slo_target)},
                "SLOWarning":         {"type": "value", "value": str(slo_warning)},
                "ManagementZone":     {"type": "environment", "name": "ManagementZone"},
            },
            "skip": False,
        },
    }


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> int:
    parser = argparse.ArgumentParser(description="Generate per-endpoint Monaco SLO configs")
    parser.add_argument(
        "--endpoints",
        default="observability/slos/endpoints/critical-endpoints.yaml",
        help="Path to critical-endpoints.yaml",
    )
    parser.add_argument(
        "--env-file",
        default="observability/environments/prod.yaml",
        help="Monaco environment YAML to read default SLO targets from",
    )
    parser.add_argument(
        "--output-dir",
        default="observability/configs/slos/endpoints/generated",
        help="Directory to write generated Monaco config files into",
    )
    args = parser.parse_args()

    endpoints_file = Path(args.endpoints)
    if not endpoints_file.exists():
        print(f"ERROR: endpoints file not found: {endpoints_file}", file=sys.stderr)
        return 1

    data = yaml.safe_load(endpoints_file.read_text()) or {}
    service = data.get("service")
    endpoints = data.get("endpoints", [])

    if not service:
        print("ERROR: 'service' key missing from critical-endpoints.yaml", file=sys.stderr)
        return 1

    if not endpoints:
        print("No endpoints defined — nothing to generate.")
        return 0

    defaults = _load_env_defaults(Path(args.env_file))
    output_dir = Path(args.output_dir)
    output_dir.mkdir(parents=True, exist_ok=True)

    # Write a .gitkeep so the generated/ dir is tracked even when empty
    (output_dir / ".gitkeep").touch()

    generated_files: list[str] = []
    errors: list[str] = []

    for ep in endpoints:
        method = ep.get("method", "").upper()
        path = ep.get("path", "")

        if not method or not path:
            errors.append(f"Endpoint missing method or path: {ep}")
            continue

        ep_id = ep.get("id") or _safe_id(method, path)

        # ── Availability SLO ───────────────────────────────────────────
        avail_cfg = _build_availability_config(service, ep, defaults)
        avail_doc = {"configs": [avail_cfg]}
        avail_path = output_dir / f"{ep_id}-availability.yaml"
        avail_path.write_text(
            f"# Generated by generate-endpoint-slos.py — do not edit by hand.\n"
            f"# Source: {endpoints_file}\n"
            + yaml.dump(avail_doc, default_flow_style=False, sort_keys=False)
        )
        generated_files.append(str(avail_path))

        # ── Latency p99 SLO ───────────────────────────────────────────
        lat_cfg = _build_latency_config(service, ep, defaults)
        lat_doc = {"configs": [lat_cfg]}
        lat_path = output_dir / f"{ep_id}-latency.yaml"
        lat_path.write_text(
            f"# Generated by generate-endpoint-slos.py — do not edit by hand.\n"
            f"# Source: {endpoints_file}\n"
            + yaml.dump(lat_doc, default_flow_style=False, sort_keys=False)
        )
        generated_files.append(str(lat_path))

        print(f"  Generated: {ep_id}  ({method} {path})")

    print(f"\nGenerated {len(generated_files)} files in {output_dir}/")

    if errors:
        for e in errors:
            print(f"  ERROR: {e}", file=sys.stderr)
        return 1

    return 0


if __name__ == "__main__":
    sys.exit(main())
