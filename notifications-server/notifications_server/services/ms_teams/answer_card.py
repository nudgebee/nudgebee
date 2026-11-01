"""Nubi's chat answers as Adaptive Cards: text as TextBlocks (Teams' markdown
subset), markdown tables as Table elements, and code blocks as monospace text."""

import re
from typing import List

_TABLE_SEPARATOR = re.compile(r"^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?\s*$")
_HEADING = re.compile(r"^\s*#{1,6}\s+(.+?)\s*#*\s*$")


def answer_card(markdown: str, actions: List[dict]) -> dict:
    return {
        "type": "AdaptiveCard",
        "$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
        # Table needs 1.5.
        "version": "1.5",
        "msteams": {"width": "Full"},
        "body": _blocks(markdown or ""),
        "actions": actions,
    }


def _blocks(markdown: str) -> List[dict]:
    lines = markdown.replace("\r\n", "\n").split("\n")
    blocks, paragraph = [], []

    def flush():
        text = "\n".join(paragraph).strip()
        if text:
            blocks.append({"type": "TextBlock", "text": text, "wrap": True})
        paragraph.clear()

    i = 0
    while i < len(lines):
        line = lines[i]
        if line.strip().startswith("```"):
            flush()
            end = next((j for j in range(i + 1, len(lines)) if lines[j].strip().startswith("```")), len(lines))
            blocks.append(_code("\n".join(lines[i + 1 : end])))
            i = end + 1
        elif _is_table_start(lines, i):
            flush()
            header = _cells(line)
            i += 2
            rows = []
            while i < len(lines) and lines[i].strip().startswith("|"):
                rows.append(_cells(lines[i]))
                i += 1
            blocks.append(_table(header, rows))
        elif not line.strip():
            flush()
            i += 1
        else:
            heading = _HEADING.match(line)
            paragraph.append(f"**{heading.group(1)}**" if heading else line)
            i += 1
    flush()
    return blocks


def _is_table_start(lines, i) -> bool:
    return lines[i].strip().startswith("|") and i + 1 < len(lines) and bool(_TABLE_SEPARATOR.match(lines[i + 1]))


def _cells(line: str) -> List[str]:
    line = line.strip()
    if line.startswith("|"):
        line = line[1:]
    if line.endswith("|") and not line.endswith("\\|"):
        line = line[:-1]
    return [cell.strip().replace("\\|", "|") for cell in re.split(r"(?<!\\)\|", line)]


def _table(header: List[str], rows: List[List[str]]) -> dict:
    width = len(header)

    def row(cells, bold=False):
        cells = (cells + [""] * width)[:width]
        return {
            "type": "TableRow",
            "cells": [
                {
                    "type": "TableCell",
                    "items": [{"type": "TextBlock", "text": c, "wrap": True, **({"weight": "Bolder"} if bold else {})}],
                }
                for c in cells
            ],
        }

    return {
        "type": "Table",
        "firstRowAsHeader": True,
        "showGridLines": True,
        "columns": [{"width": 1} for _ in range(width)],
        "rows": [row(header, bold=True)] + [row(r) for r in rows],
    }


def _code(code: str) -> dict:
    # A TextRun isn't parsed as markdown, so the code shows as written.
    return {
        "type": "Container",
        "style": "emphasis",
        "items": [{"type": "RichTextBlock", "inlines": [{"type": "TextRun", "text": code, "fontType": "Monospace"}]}],
    }
