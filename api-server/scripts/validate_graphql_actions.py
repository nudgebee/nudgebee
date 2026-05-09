import os
import re
import sys
from collections import Counter

import yaml


def check_duplicates(names, label):
    """Check for duplicates in a list of names. Returns True if duplicates found."""
    dupes = {name: count for name, count in Counter(names).items() if count > 1}
    if dupes:
        print(
            f"Error: Found {len(dupes)} duplicate {label}:",
            file=sys.stderr,
        )
        for name, count in sorted(dupes.items()):
            print(f"  - {name} (appears {count} times)", file=sys.stderr)
        return True
    return False


def validate_graphql(file_path):
    try:
        with open(file_path, "r") as f:
            content = f.read()
    except FileNotFoundError:
        print(f"Error: File not found at {file_path}", file=sys.stderr)
        sys.exit(1)

    has_errors = False

    # 1. Check duplicate actions (Query/Mutation)
    action_pattern = re.compile(
        r"type\s+(?:Query|Mutation)\s*\{\s*(\w+)\s*\(", re.MULTILINE
    )
    actions = action_pattern.findall(content)

    if not actions:
        print("No actions found in actions.graphql.", file=sys.stderr)
        sys.exit(1)

    if check_duplicates(actions, "action(s) in actions.graphql"):
        has_errors = True

    # 2. Check duplicate type definitions (type/input/enum, excluding Query/Mutation)
    type_pattern = re.compile(
        r"^(?:type|input|enum)\s+(\w+)\s*\{", re.MULTILINE
    )
    types = [
        name for name in type_pattern.findall(content)
        if name not in ("Query", "Mutation")
    ]

    if check_duplicates(types, "type definition(s) in actions.graphql"):
        has_errors = True

    if not has_errors:
        print(
            f"actions.graphql: {len(actions)} actions, {len(types)} type definitions, no duplicates."
        )

    return has_errors


def validate_yaml(file_path):
    try:
        with open(file_path, "r") as f:
            data = yaml.safe_load(f)
    except FileNotFoundError:
        print(f"Error: File not found at {file_path}", file=sys.stderr)
        sys.exit(1)

    has_errors = False

    # 1. Check duplicate action names
    actions = [a["name"] for a in data.get("actions", [])]
    if check_duplicates(actions, "action(s) in actions.yaml"):
        has_errors = True

    # 2. Check duplicate custom type names within each category
    custom_types = data.get("custom_types", {})
    for category, items in custom_types.items():
        if isinstance(items, list):
            names = [i["name"] for i in items if isinstance(i, dict) and "name" in i]
            if check_duplicates(names, f"custom_types.{category} name(s) in actions.yaml"):
                has_errors = True

    if not has_errors:
        type_count = sum(
            len(v) for v in custom_types.values() if isinstance(v, list)
        )
        print(
            f"actions.yaml: {len(actions)} actions, {type_count} custom types, no duplicates."
        )

    return has_errors


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print(
            "Usage: python validate_graphql_actions.py <actions.graphql> [actions.yaml]",
            file=sys.stderr,
        )
        sys.exit(1)

    has_errors = False
    has_errors |= validate_graphql(sys.argv[1])

    # Auto-detect actions.yaml in the same directory if not explicitly provided
    if len(sys.argv) >= 3:
        yaml_path = sys.argv[2]
    else:
        yaml_path = os.path.join(os.path.dirname(sys.argv[1]), "actions.yaml")

    if os.path.exists(yaml_path):
        has_errors |= validate_yaml(yaml_path)

    if has_errors:
        sys.exit(1)
    else:
        print("Validation passed.")
