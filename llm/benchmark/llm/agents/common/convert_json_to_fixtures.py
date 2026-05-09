#!/usr/bin/env python3
"""Convert legacy JSON benchmark data files to YAML fixture directories.

Usage:
    python -m agents.common.convert_json_to_fixtures \\
        --json-file agents/kubectl/data/benchmark_k8s.json \\
        --fixtures-dir agents/kubectl/fixtures \\
        --default-tags command kubernetes

Each JSON entry becomes a directory: fixtures/NNN_slug/test_case.yaml

JSON format expected:
    [
        {
            "query": "...",
            "contexts": ["..."],
            "ground_truths": "..." or ["..."]
        },
        ...
    ]
"""

import argparse
import json
import re
from pathlib import Path

import yaml


def slugify(text: str, max_len: int = 40) -> str:
    """Convert text to a filesystem-safe slug."""
    text = text.lower().strip()
    text = re.sub(r"[^a-z0-9\s-]", "", text)
    text = re.sub(r"[\s-]+", "_", text)
    return text[:max_len].rstrip("_")


def convert(json_file: str, fixtures_dir: str, default_tags: list):
    json_path = Path(json_file)
    fixtures_path = Path(fixtures_dir)
    fixtures_path.mkdir(parents=True, exist_ok=True)

    with open(json_path) as f:
        data = json.load(f)

    print(f"Converting {len(data)} entries from {json_path} -> {fixtures_path}/")

    for i, entry in enumerate(data):
        query = entry.get("query", "")
        ground_truths = entry.get("ground_truths", entry.get("ground_truth", []))

        # Normalize to list
        if isinstance(ground_truths, str):
            ground_truths = [ground_truths]

        slug = slugify(query)
        dir_name = f"{i + 1:03d}_{slug}"
        case_dir = fixtures_path / dir_name
        case_dir.mkdir(parents=True, exist_ok=True)

        test_case = {
            "user_prompt": query,
            "expected_output": ground_truths,
            "tags": list(default_tags),
        }

        yaml_path = case_dir / "test_case.yaml"
        with open(yaml_path, "w") as f:
            yaml.dump(
                test_case,
                f,
                default_flow_style=False,
                sort_keys=False,
                allow_unicode=True,
            )

        print(f"  [{i + 1:3d}] {dir_name}/test_case.yaml")

    print(f"\nDone. {len(data)} fixtures created in {fixtures_path}/")


def main():
    parser = argparse.ArgumentParser(
        description="Convert JSON benchmark data to YAML fixtures"
    )
    parser.add_argument("--json-file", required=True, help="Path to JSON data file")
    parser.add_argument(
        "--fixtures-dir", required=True, help="Output fixtures directory"
    )
    parser.add_argument(
        "--default-tags", nargs="*", default=["command"], help="Default tags"
    )
    args = parser.parse_args()
    convert(args.json_file, args.fixtures_dir, args.default_tags)


if __name__ == "__main__":
    main()
