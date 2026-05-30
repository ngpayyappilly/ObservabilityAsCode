#!/usr/bin/env python3
"""
ddu-estimator.py — Estimate Dynatrace Data Unit (DDU) consumption for OaC configs.

Parses all Monaco config YAML files in the observability/ folder and computes
a monthly DDU estimate. Used in the PR validation pipeline to block configs
that would exceed the per-application DDU cap.

Usage:
    python scripts/ddu-estimator.py \
        --manifest observability/manifest.yaml \
        --environment staging \
        --cap 5000 \
        --output-json /tmp/ddu-estimate.json
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path

import yaml

# ---------------------------------------------------------------------------
# DDU rates (monthly basis = 43200 minutes)
# ---------------------------------------------------------------------------
MINUTES_PER_MONTH = 43_200

# Per-entity monthly DDU rates
DDU_RATES: dict[str, float] = {
    "slo": 0.001,            # per SLO per minute
    "dashboard": 0.0,        # dashboards are free
    "anomaly-detection-metrics": 0.0,  # metric events are free
    "synthetic-monitor": None,         # computed from frequency (see below)
    "log.metrics": 0.001,              # per log metric per minute
}

# Synthetic: 0.1 DDU per execution. Executions = MINUTES_PER_MONTH / freq_min
DDU_PER_SYNTHETIC_EXECUTION = 0.1


def _find_config_yamls(observability_dir: Path) -> list[Path]:
    """Return all Monaco config descriptor YAML files (not manifest, not environments)."""
    skip_dirs = {"environments"}
    results = []
    for yaml_file in observability_dir.rglob("*.yaml"):
        if yaml_file.parent.name in skip_dirs:
            continue
        if yaml_file.name == "manifest.yaml":
            continue
        results.append(yaml_file)
    return results


def _parse_api_type(config_doc: dict) -> str | None:
    """Extract the Monaco API type from a config descriptor document."""
    for config in config_doc.get("configs", []):
        type_block = config.get("type", {})
        if "api" in type_block:
            return type_block["api"]
        if "settings" in type_block:
            schema = type_block["settings"].get("schema", "")
            if "log.metrics" in schema:
                return "log.metrics"
    return None


def _get_synthetic_frequency(config_doc: dict) -> float:
    """Extract SyntheticFreqMin parameter value from a synthetic-monitor config."""
    for config in config_doc.get("configs", []):
        params = config.get("config", {}).get("parameters", {})
        freq = params.get("SyntheticFreqMin", {})
        if isinstance(freq, dict):
            val = freq.get("value", "5")
        else:
            val = str(freq)
        try:
            return float(val)
        except (ValueError, TypeError):
            return 5.0  # safe default
    return 5.0


def estimate_ddus(observability_dir: Path) -> list[dict]:
    """
    Walk all Monaco config YAMLs and return a breakdown list:
    [{"type": str, "id": str, "count": int, "rate_per_entity": float, "total_ddu": float}]
    """
    breakdown: list[dict] = []
    yaml_files = _find_config_yamls(observability_dir)

    for yaml_file in yaml_files:
        try:
            docs = list(yaml.safe_load_all(yaml_file.read_text()))
        except yaml.YAMLError as exc:
            print(f"WARNING: Could not parse {yaml_file}: {exc}", file=sys.stderr)
            continue

        for doc in docs:
            if not isinstance(doc, dict) or "configs" not in doc:
                continue

            api_type = _parse_api_type(doc)
            if api_type is None:
                continue

            count = len(doc.get("configs", []))

            if api_type == "synthetic-monitor":
                freq_min = _get_synthetic_frequency(doc)
                executions = MINUTES_PER_MONTH / freq_min
                rate = DDU_PER_SYNTHETIC_EXECUTION * executions
                total = rate * count
                breakdown.append({
                    "file": str(yaml_file),
                    "type": api_type,
                    "count": count,
                    "freq_min": freq_min,
                    "executions_per_month": executions,
                    "rate_per_entity": rate,
                    "total_ddu": total,
                })
            elif api_type in DDU_RATES:
                rate = DDU_RATES[api_type]
                if rate is None:
                    continue
                total = rate * MINUTES_PER_MONTH * count
                breakdown.append({
                    "file": str(yaml_file),
                    "type": api_type,
                    "count": count,
                    "rate_per_entity": rate,
                    "total_ddu": total,
                })

    return breakdown


def main() -> int:
    parser = argparse.ArgumentParser(description="Estimate DDU consumption for OaC configs")
    parser.add_argument("--manifest", default="observability/manifest.yaml",
                        help="Path to Monaco manifest.yaml")
    parser.add_argument("--environment", default="staging",
                        help="Environment name (informational only)")
    parser.add_argument("--cap", type=float, default=5000.0,
                        help="Max allowable DDU per app per month")
    parser.add_argument("--output-json", default=None,
                        help="Write JSON breakdown to this path")
    args = parser.parse_args()

    manifest_path = Path(args.manifest)
    if not manifest_path.exists():
        print(f"ERROR: Manifest not found: {manifest_path}", file=sys.stderr)
        return 1

    observability_dir = manifest_path.parent
    breakdown = estimate_ddus(observability_dir)
    total_ddu = sum(item["total_ddu"] for item in breakdown)

    output = {
        "environment": args.environment,
        "cap": args.cap,
        "total_ddu": total_ddu,
        "breakdown": breakdown,
    }

    # Always print summary to stdout for pipeline visibility
    print(f"DDU Estimate — environment: {args.environment}")
    print(f"{'Type':<35} {'Count':>5} {'DDU/month':>12}")
    print("-" * 56)
    for item in breakdown:
        print(f"{item['type']:<35} {item['count']:>5} {item['total_ddu']:>12.2f}")
    print("-" * 56)
    print(f"{'TOTAL':<35} {'':>5} {total_ddu:>12.2f}")
    print(f"Cap: {args.cap:.2f}")

    if args.output_json:
        Path(args.output_json).write_text(json.dumps(output, indent=2))
        print(f"\nJSON breakdown written to: {args.output_json}")

    if total_ddu > args.cap:
        print(
            f"\nERROR: Estimated DDU ({total_ddu:.2f}) exceeds cap ({args.cap:.2f}). "
            "Reduce synthetic monitor frequency or remove unused monitors.",
            file=sys.stderr,
        )
        return 1

    print(f"\nOK: Estimated DDU ({total_ddu:.2f}) is within cap ({args.cap:.2f}).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
