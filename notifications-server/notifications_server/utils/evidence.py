def flatten_evidence_rows(data: dict) -> dict | None:
    if isinstance(data, dict) and "rows" in data:
        try:
            return {row[0]: row[1] for row in data["rows"] if len(row) >= 2}
        except Exception:
            return None
    return None
