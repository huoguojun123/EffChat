from __future__ import annotations

import html


def serialize(rows: list[list[object]]) -> str:
    """Serialize irregular rows into one structurally stable GFM table."""
    cleaned = [[_encode_cell(cell) for cell in row] for row in rows]
    cleaned = [row for row in cleaned if any(cell for cell in row)]
    if not cleaned:
        return ""
    width = max(len(row) for row in cleaned)
    normalized = [row + [""] * (width - len(row)) for row in cleaned]
    header = normalized[0]
    body = normalized[1:] or [[""] * width]
    lines = [
        "| " + " | ".join(header) + " |",
        "| " + " | ".join(["---"] * width) + " |",
    ]
    lines.extend("| " + " | ".join(row) + " |" for row in body)
    return "\n".join(lines)


def _encode_cell(value: object) -> str:
    # Escape user HTML first so only the entity inserted for a real line break
    # can be decoded by remark-gfm. Backslashes must be doubled before pipes
    # are escaped; this preserves an existing literal "\|" as backslash+pipe.
    text = "" if value is None else str(value).strip()
    text = text.replace("\r\n", "\n").replace("\r", "\n")
    text = html.escape(text, quote=False)
    return text.replace("\\", "\\\\").replace("|", "\\|").replace("\n", "&#10;")
