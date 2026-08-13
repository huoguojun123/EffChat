from __future__ import annotations

import csv
import io
import struct
import zipfile

from fastapi import HTTPException


OFFICE_MAX_ENTRIES = 4096
OFFICE_MAX_CENTRAL_DIRECTORY_BYTES = 4 * 1024 * 1024
OFFICE_MAX_UNCOMPRESSED_BYTES = 64 * 1024 * 1024
OFFICE_MAX_ENTRY_BYTES = 32 * 1024 * 1024
OFFICE_MAX_COMPRESSION_RATIO = 100

CSV_ALLOWED_DELIMITERS = ",\t;|"
CSV_MAX_FIELD_CHARS = 1024 * 1024
CSV_MAX_COLUMNS = 256
CSV_MAX_ROWS = 100_000
CSV_MAX_CELLS = 500_000

# Python's process default is only 128 KiB, which rejects ordinary long cells
# far below EffChat's upload and output budgets. This explicit process-wide
# ceiling is paired with per-document row, column, cell, and content limits.
csv.field_size_limit(CSV_MAX_FIELD_CHARS)


def validate_office_archive(data: bytes) -> None:
    """Reject Office ZIPs whose central-directory facts exceed safe bounds."""
    declared_entries = _preflight_zip_directory(data)
    try:
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            entries = archive.infolist()
    except zipfile.BadZipFile as exc:
        raise HTTPException(status_code=422, detail="office_archive_invalid") from exc

    if len(entries) > OFFICE_MAX_ENTRIES:
        raise HTTPException(status_code=413, detail="office_archive_entry_limit_exceeded")
    if len(entries) != declared_entries:
        raise HTTPException(status_code=422, detail="office_archive_directory_mismatch")

    total_uncompressed = 0
    for entry in entries:
        if entry.is_dir():
            continue
        if entry.file_size > OFFICE_MAX_ENTRY_BYTES:
            raise HTTPException(status_code=413, detail="office_archive_file_limit_exceeded")
        total_uncompressed += entry.file_size
        if total_uncompressed > OFFICE_MAX_UNCOMPRESSED_BYTES:
            raise HTTPException(status_code=413, detail="office_archive_size_limit_exceeded")
        if entry.file_size > 0:
            ratio = entry.file_size / max(entry.compress_size, 1)
            if ratio > OFFICE_MAX_COMPRESSION_RATIO:
                raise HTTPException(status_code=413, detail="office_archive_ratio_limit_exceeded")


def _preflight_zip_directory(data: bytes) -> int:
    """Read the small EOCD record before ZipFile allocates all ZipInfo entries."""
    signature = b"PK\x05\x06"
    minimum_size = 22
    search_start = max(0, len(data) - (65_535 + minimum_size))
    search_end = len(data)
    record: tuple[bytes, int, int, int, int, int, int, int] | None = None
    record_offset = -1

    while search_end >= minimum_size:
        offset = data.rfind(signature, search_start, search_end)
        if offset < 0:
            break
        if offset + minimum_size <= len(data):
            candidate = struct.unpack_from("<4s4H2LH", data, offset)
            if offset + minimum_size + candidate[-1] == len(data):
                record = candidate
                record_offset = offset
                break
        search_end = offset

    if record is None:
        raise HTTPException(status_code=422, detail="office_archive_invalid")

    _, disk_number, directory_disk, disk_entries, total_entries, directory_size, directory_offset, _ = record
    if disk_number != 0 or directory_disk != 0 or disk_entries != total_entries:
        raise HTTPException(status_code=422, detail="office_archive_multidisk_unsupported")
    if total_entries == 0xFFFF or directory_size == 0xFFFFFFFF or directory_offset == 0xFFFFFFFF:
        raise HTTPException(status_code=413, detail="office_archive_zip64_unsupported")
    if total_entries > OFFICE_MAX_ENTRIES:
        raise HTTPException(status_code=413, detail="office_archive_entry_limit_exceeded")
    if directory_size > OFFICE_MAX_CENTRAL_DIRECTORY_BYTES:
        raise HTTPException(status_code=413, detail="office_archive_directory_limit_exceeded")
    if directory_offset + directory_size > record_offset:
        raise HTTPException(status_code=422, detail="office_archive_invalid")
    return total_entries


def read_bounded_csv(text: str, max_content_bytes: int) -> list[list[str]]:
    """Parse CSV while bounding every allocation dimension owned by the reader."""
    dialect = _detect_csv_dialect(text[:4096])
    reader = csv.reader(io.StringIO(text), dialect)
    rows: list[list[str]] = []
    total_cells = 0
    total_field_bytes = 0
    try:
        for row_number, row in enumerate(reader, start=1):
            if row_number > CSV_MAX_ROWS:
                raise HTTPException(status_code=413, detail="csv_row_limit_exceeded")
            if len(row) > CSV_MAX_COLUMNS:
                raise HTTPException(status_code=413, detail="csv_column_limit_exceeded")
            total_cells += len(row)
            if total_cells > CSV_MAX_CELLS:
                raise HTTPException(status_code=413, detail="csv_cell_limit_exceeded")
            total_field_bytes += sum(len(cell.encode("utf-8")) for cell in row)
            if total_field_bytes > max_content_bytes:
                raise HTTPException(status_code=413, detail="csv_content_limit_exceeded")
            if any(cell.strip() for cell in row):
                rows.append(row)
    except csv.Error as exc:
        if "field larger than field limit" in str(exc).lower():
            raise HTTPException(status_code=413, detail="csv_field_limit_exceeded") from exc
        raise HTTPException(status_code=422, detail="csv_parse_failed") from exc
    return rows


def _detect_csv_dialect(sample: str) -> csv.Dialect:
    # No allowed delimiter means a valid single-column document. Once a
    # delimiter candidate exists, ambiguity is rejected instead of silently
    # treating arbitrary content characters as a separator.
    if not any(delimiter in sample for delimiter in CSV_ALLOWED_DELIMITERS):
        return csv.excel
    try:
        return csv.Sniffer().sniff(sample, delimiters=CSV_ALLOWED_DELIMITERS)
    except csv.Error as exc:
        raise HTTPException(status_code=422, detail="csv_delimiter_uncertain") from exc
