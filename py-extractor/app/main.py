from __future__ import annotations

import asyncio
import http.client
import importlib
import io
import json
import logging
import os
import tempfile
import time
import zipfile
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable
from urllib import error as urlerror
from urllib.parse import urlsplit
from urllib import request as urlrequest

import pdfplumber
from fastapi import FastAPI, File, Form, Header, HTTPException, Query, UploadFile
from starlette.concurrency import run_in_threadpool

from app import docx_content, markdown_table, pptx_content, resource_limits, xlsx_content


app = FastAPI(title="EffChat Extractor", version="0.4.1-beta.6")
logger = logging.getLogger("uvicorn.error")

MAX_UPLOAD_BYTES = int(os.getenv("EXTRACTOR_MAX_UPLOAD_BYTES", str(25 * 1024 * 1024)))
MAX_OUTPUT_BYTES = int(os.getenv("EXTRACTOR_MAX_OUTPUT_BYTES", str(5 * 1024 * 1024)))
MINERU_MAX_BYTES = 200 * 1024 * 1024
MINERU_MAX_PAGES = 200
MINERU_MAX_ZIP_BYTES = int(os.getenv("MINERU_MAX_ZIP_BYTES", str(100 * 1024 * 1024)))
MINERU_UPLOAD_TIMEOUT_SECONDS = int(os.getenv("MINERU_UPLOAD_TIMEOUT_SECONDS", "300"))
MINERU_DEFAULT_BASE_URL = "https://mineru.net"
LOCAL_PARSE_CONCURRENCY = 2
LOCAL_PARSE_QUEUE_TIMEOUT_SECONDS = 5.0
LOCAL_PARSE_SLOTS = asyncio.Semaphore(LOCAL_PARSE_CONCURRENCY)


@dataclass
class ExtractedDocument:
    text: str
    parser: str
    page_count: int = 0
    paragraph_count: int = 0
    table_count: int = 0
    warnings: list[str] = field(default_factory=list)


@app.get("/health")
async def health() -> dict[str, str]:
    return {"status": "ok"}


@app.post("/extract")
async def extract(
    file: UploadFile = File(...),
    filename: str = Form(default=""),
    content_type: str = Form(default=""),
) -> dict[str, object]:
    started_at = time.perf_counter()
    safe_name = Path(filename or file.filename or "file").name
    declared_type = content_type or file.content_type or ""
    logger.info(
        "[py-extractor] extract start filename=%s declared_type=%s upload_type=%s",
        safe_name,
        declared_type or "-",
        file.content_type or "-",
    )

    data = await file.read(MAX_UPLOAD_BYTES + 1)
    if len(data) > MAX_UPLOAD_BYTES:
        logger.warning(
            "[py-extractor] extract rejected filename=%s reason=file_too_large bytes=%d limit=%d duration_ms=%d",
            safe_name,
            len(data),
            MAX_UPLOAD_BYTES,
            elapsed_ms(started_at),
        )
        raise HTTPException(status_code=413, detail=f"file too large (max {MAX_UPLOAD_BYTES} bytes)")

    ext = Path(safe_name).suffix.lower()
    parser = parser_for(ext, declared_type)
    if parser is None:
        logger.warning(
            "[py-extractor] extract rejected filename=%s reason=unsupported_type declared_type=%s bytes=%d duration_ms=%d",
            safe_name,
            declared_type or "-",
            len(data),
            elapsed_ms(started_at),
        )
        raise HTTPException(status_code=415, detail="unsupported document type")
    parser_name = getattr(parser, "__name__", "unknown")

    try:
        doc = await run_local_parser(parser, data, safe_name)
    except HTTPException as exc:
        logger.warning(
            "[py-extractor] extract rejected filename=%s parser=%s reason=%s status=%d bytes=%d duration_ms=%d",
            safe_name,
            parser_name,
            exc.detail,
            exc.status_code,
            len(data),
            elapsed_ms(started_at),
        )
        raise
    except Exception as exc:
        logger.exception(
            "[py-extractor] extract failed filename=%s parser=%s bytes=%d duration_ms=%d",
            safe_name,
            parser_name,
            len(data),
            elapsed_ms(started_at),
        )
        raise HTTPException(status_code=422, detail=f"extract failed: {exc}") from exc

    text = normalize_markdown(doc.text)
    if not text.strip():
        logger.warning(
            "[py-extractor] extract rejected filename=%s parser=%s reason=empty_text bytes=%d duration_ms=%d",
            safe_name,
            doc.parser,
            len(data),
            elapsed_ms(started_at),
        )
        raise HTTPException(status_code=422, detail="no readable text extracted")

    encoded = text.encode("utf-8")
    if len(encoded) > MAX_OUTPUT_BYTES:
        logger.warning(
            "[py-extractor] extract rejected filename=%s parser=%s reason=output_too_large output_bytes=%d limit=%d duration_ms=%d",
            safe_name,
            doc.parser,
            len(encoded),
            MAX_OUTPUT_BYTES,
            elapsed_ms(started_at),
        )
        raise HTTPException(status_code=413, detail=f"extracted text too large (max {MAX_OUTPUT_BYTES} bytes)")

    logger.info(
        "[py-extractor] extract success filename=%s parser=%s bytes=%d output_bytes=%d pages=%d paragraphs=%d tables=%d warnings=%d duration_ms=%d",
        safe_name,
        doc.parser,
        len(data),
        len(encoded),
        doc.page_count,
        doc.paragraph_count or count_paragraphs(text),
        doc.table_count,
        len(doc.warnings),
        elapsed_ms(started_at),
    )
    return {
        "text": text,
        "parser": doc.parser,
        "token_estimate": estimate_tokens(text),
        "page_count": doc.page_count,
        "paragraph_count": doc.paragraph_count or count_paragraphs(text),
        "table_count": doc.table_count,
        "warnings": doc.warnings,
    }


async def run_local_parser(
    parser: Callable[[bytes, str], ExtractedDocument],
    data: bytes,
    safe_name: str,
) -> ExtractedDocument:
    # Office/PDF/CSV parsers are synchronous and may spend meaningful time in
    # Python or native libraries. Two slots keep that work off the sole uvicorn
    # event loop without allowing the default thread pool to multiply each
    # request's bounded memory across dozens of simultaneous parsers.
    try:
        await asyncio.wait_for(LOCAL_PARSE_SLOTS.acquire(), timeout=LOCAL_PARSE_QUEUE_TIMEOUT_SECONDS)
    except TimeoutError as exc:
        raise HTTPException(status_code=503, detail="extractor_busy") from exc
    try:
        return await run_in_threadpool(parser, data, safe_name)
    finally:
        LOCAL_PARSE_SLOTS.release()


@app.post("/ocr/mineru/start")
async def start_mineru_ocr(
    file: UploadFile = File(...),
    filename: str = Form(default=""),
    base_url: str = Form(default=MINERU_DEFAULT_BASE_URL),
    api_key: str = Form(default=""),
) -> dict[str, object]:
    started_at = time.perf_counter()
    safe_name = Path(filename or file.filename or "document.pdf").name
    token = api_key.strip()
    if not token:
        raise HTTPException(status_code=400, detail="mineru_api_key_required")
    data = await file.read(MINERU_MAX_BYTES + 1)
    return await run_in_threadpool(start_mineru_ocr_bytes, data, safe_name, base_url, token, started_at)


def start_mineru_ocr_bytes(
    data: bytes,
    safe_name: str,
    base_url: str,
    token: str,
    started_at: float,
) -> dict[str, object]:
    logger.info("[py-extractor] mineru_start filename=%s bytes=%d", safe_name, len(data))
    if len(data) > MINERU_MAX_BYTES:
        logger.warning("[py-extractor] mineru_rejected filename=%s reason=file_too_large bytes=%d limit=%d", safe_name, len(data), MINERU_MAX_BYTES)
        raise HTTPException(status_code=413, detail=f"ocr_file_too_large: max {MINERU_MAX_BYTES} bytes")
    page_count = count_pdf_pages(data)
    if page_count > MINERU_MAX_PAGES:
        logger.warning("[py-extractor] mineru_rejected filename=%s reason=page_limit pages=%d limit=%d", safe_name, page_count, MINERU_MAX_PAGES)
        raise HTTPException(status_code=400, detail=f"ocr_page_limit_exceeded: max {MINERU_MAX_PAGES} pages")

    root = normalize_mineru_base_url(base_url)
    logger.info("[py-extractor] mineru_submit_request filename=%s pages=%d endpoint=/api/v4/file-urls/batch", safe_name, page_count)
    payload = {
        "files": [{"name": safe_name, "is_ocr": True}],
        "model_version": "vlm",
        "language": "ch",
        "enable_table": True,
        "enable_formula": True,
    }
    submit = mineru_json_request("POST", f"{root}/api/v4/file-urls/batch", payload, token)
    body = submit.get("data") if isinstance(submit.get("data"), dict) else {}
    task_id = str(body.get("batch_id") or "").strip()
    file_urls = body.get("file_urls") if isinstance(body.get("file_urls"), list) else []
    file_url = str(file_urls[0] if file_urls else "").strip()
    if not task_id or not file_url:
        logger.warning("[py-extractor] mineru_submit_invalid filename=%s pages=%d duration_ms=%d", safe_name, page_count, elapsed_ms(started_at))
        raise HTTPException(status_code=502, detail="mineru returned no task upload url")
    logger.info("[py-extractor] mineru_upload_start filename=%s task=%s bytes=%d", safe_name, task_id, len(data))
    mineru_put_file(file_url, data)
    logger.info("[py-extractor] mineru_start_success filename=%s task=%s pages=%d duration_ms=%d", safe_name, task_id, page_count, elapsed_ms(started_at))
    return {"task_id": task_id, "state": "ocr_running", "page_count": page_count}


@app.get("/ocr/mineru/tasks/{task_id}")
async def get_mineru_ocr_task(
    task_id: str,
    base_url: str = Query(default=MINERU_DEFAULT_BASE_URL),
    mineru_token: str = Header(default="", alias="X-MinerU-Token"),
) -> dict[str, object]:
    started_at = time.perf_counter()
    root = normalize_mineru_base_url(base_url)
    token = mineru_token.strip()
    if not token:
        raise HTTPException(status_code=400, detail="mineru_api_key_required")
    return await run_in_threadpool(get_mineru_ocr_task_blocking, task_id, root, token, started_at)


def get_mineru_ocr_task_blocking(
    task_id: str,
    root: str,
    token: str,
    started_at: float,
) -> dict[str, object]:
    logger.info("[py-extractor] mineru_poll_request task=%s", task_id)
    result = mineru_json_request("GET", f"{root}/api/v4/extract-results/batch/{task_id}", None, token)
    body = result.get("data") if isinstance(result.get("data"), dict) else {}
    items = body.get("extract_result") if isinstance(body.get("extract_result"), list) else []
    item = items[0] if items and isinstance(items[0], dict) else {}
    state = str(item.get("state") or "").strip()
    if state == "done":
        full_zip_url = str(item.get("full_zip_url") or "").strip()
        if not full_zip_url:
            logger.warning("[py-extractor] mineru_poll_invalid task=%s reason=missing_full_zip_url duration_ms=%d", task_id, elapsed_ms(started_at))
            raise HTTPException(status_code=502, detail="mineru finished without full_zip_url")
        logger.info("[py-extractor] mineru_markdown_download_start task=%s", task_id)
        markdown = mineru_download_full_md(full_zip_url)
        logger.info("[py-extractor] mineru_poll_ready task=%s chars=%d duration_ms=%d", task_id, len(markdown), elapsed_ms(started_at))
        return {"state": "ready", "markdown": markdown, "token_estimate": estimate_tokens(markdown)}
    if state == "failed":
        err_msg = str(item.get("err_msg") or result.get("msg") or "MinerU OCR failed")
        err_code = str(item.get("err_code") or "mineru_failed")
        logger.warning("[py-extractor] mineru_poll_failed task=%s error_type=%s duration_ms=%d", task_id, err_code, elapsed_ms(started_at))
        return {"state": "failed", "error": err_msg, "error_type": err_code}
    logger.info("[py-extractor] mineru_poll_running task=%s state=%s duration_ms=%d", task_id, state or "-", elapsed_ms(started_at))
    return {"state": "ocr_running" if state in {"waiting-file", "pending", "running", "converting"} else "ocr_pending", "mineru_state": state}


@app.post("/ocr/pdf-info")
async def inspect_pdf_for_ocr(
    file: UploadFile = File(...),
    filename: str = Form(default=""),
) -> dict[str, object]:
    started_at = time.perf_counter()
    safe_name = Path(filename or file.filename or "document.pdf").name
    data = await file.read(MINERU_MAX_BYTES + 1)
    logger.info("[py-extractor] mineru_pdf_info_start filename=%s bytes=%d", safe_name, len(data))
    if len(data) > MINERU_MAX_BYTES:
        logger.warning("[py-extractor] mineru_pdf_info_rejected filename=%s reason=file_too_large bytes=%d limit=%d", safe_name, len(data), MINERU_MAX_BYTES)
        raise HTTPException(status_code=413, detail=f"ocr_file_too_large: max {MINERU_MAX_BYTES} bytes")
    page_count = await run_in_threadpool(count_pdf_pages, data)
    logger.info("[py-extractor] mineru_pdf_info_success filename=%s pages=%d duration_ms=%d", safe_name, page_count, elapsed_ms(started_at))
    return {"filename": safe_name, "page_count": page_count, "max_pages": MINERU_MAX_PAGES, "max_bytes": MINERU_MAX_BYTES}


def elapsed_ms(started_at: float) -> int:
    return int((time.perf_counter() - started_at) * 1000)


def normalize_mineru_base_url(value: str) -> str:
    base = (value or MINERU_DEFAULT_BASE_URL).strip().rstrip("/")
    return base or MINERU_DEFAULT_BASE_URL


def count_pdf_pages(data: bytes) -> int:
    try:
        with pdfplumber.open(io.BytesIO(data)) as pdf:
            return len(pdf.pages)
    except Exception as exc:
        raise HTTPException(status_code=422, detail=f"ocr_page_count_failed: {exc}") from exc


def mineru_json_request(method: str, url: str, payload: dict[str, object] | None, api_key: str) -> dict[str, object]:
    started_at = time.perf_counter()
    body = json.dumps(payload).encode("utf-8") if payload is not None else None
    req = urlrequest.Request(url, data=body, method=method)
    req.add_header("Accept", "application/json")
    req.add_header("Authorization", f"Bearer {api_key.strip()}")
    if body is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urlrequest.urlopen(req, timeout=30) as resp:
            raw = resp.read(2 * 1024 * 1024)
    except urlerror.HTTPError as exc:
        raw = exc.read(4096).decode("utf-8", errors="ignore")
        logger.warning("[py-extractor] mineru_http_error method=%s status=%d duration_ms=%d", method, exc.code, elapsed_ms(started_at))
        raise HTTPException(status_code=exc.code, detail=f"mineru upstream error: {raw}") from exc
    except urlerror.URLError as exc:
        logger.warning("[py-extractor] mineru_http_failed method=%s reason=%s duration_ms=%d", method, exc.reason, elapsed_ms(started_at))
        raise HTTPException(status_code=502, detail=f"mineru request failed: {exc.reason}") from exc
    try:
        parsed = json.loads(raw.decode("utf-8"))
    except Exception as exc:
        logger.warning("[py-extractor] mineru_invalid_json method=%s duration_ms=%d", method, elapsed_ms(started_at))
        raise HTTPException(status_code=502, detail="mineru returned invalid JSON") from exc
    if not isinstance(parsed, dict):
        raise HTTPException(status_code=502, detail="mineru returned invalid response")
    if parsed.get("code") not in (0, "0"):
        logger.warning("[py-extractor] mineru_rejected method=%s code=%s duration_ms=%d", method, parsed.get("code"), elapsed_ms(started_at))
        raise HTTPException(status_code=502, detail=f"mineru rejected request: {parsed.get('msg') or parsed.get('code')}")
    logger.info("[py-extractor] mineru_http_success method=%s bytes=%d duration_ms=%d", method, len(raw), elapsed_ms(started_at))
    return parsed


def mineru_put_file(file_url: str, data: bytes) -> None:
    started_at = time.perf_counter()
    parsed = urlsplit(file_url)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise HTTPException(status_code=502, detail="mineru upload url is invalid")
    path = parsed.path or "/"
    if parsed.query:
        path = f"{path}?{parsed.query}"
    conn_cls = http.client.HTTPSConnection if parsed.scheme == "https" else http.client.HTTPConnection
    conn = conn_cls(parsed.netloc, timeout=MINERU_UPLOAD_TIMEOUT_SECONDS)
    try:
        conn.request("PUT", path, body=data, headers={"Content-Length": str(len(data))})
        resp = conn.getresponse()
        raw = resp.read(4096).decode("utf-8", errors="ignore")
        if resp.status < 200 or resp.status >= 300:
            logger.warning("[py-extractor] mineru_upload_http_error host=%s status=%d duration_ms=%d", parsed.netloc, resp.status, elapsed_ms(started_at))
            raise HTTPException(status_code=resp.status, detail=f"mineru upload failed: {raw}")
    except HTTPException:
        raise
    except OSError as exc:
        logger.warning("[py-extractor] mineru_upload_failed host=%s reason=%s duration_ms=%d", parsed.netloc, exc, elapsed_ms(started_at))
        raise HTTPException(status_code=502, detail=f"mineru upload failed: {exc}") from exc
    finally:
        conn.close()
    logger.info("[py-extractor] mineru_upload_success bytes=%d duration_ms=%d", len(data), elapsed_ms(started_at))


def mineru_download_full_md(full_zip_url: str) -> str:
    started_at = time.perf_counter()
    try:
        with urlrequest.urlopen(full_zip_url, timeout=60) as resp:
            data = resp.read(MINERU_MAX_ZIP_BYTES + 1)
    except urlerror.HTTPError as exc:
        logger.warning("[py-extractor] mineru_markdown_http_error status=%d duration_ms=%d", exc.code, elapsed_ms(started_at))
        raise HTTPException(status_code=exc.code, detail="mineru markdown download failed") from exc
    except urlerror.URLError as exc:
        logger.warning("[py-extractor] mineru_markdown_failed reason=%s duration_ms=%d", exc.reason, elapsed_ms(started_at))
        raise HTTPException(status_code=502, detail=f"mineru markdown download failed: {exc.reason}") from exc
    if len(data) > MINERU_MAX_ZIP_BYTES:
        logger.warning("[py-extractor] mineru_zip_too_large bytes=%d limit=%d duration_ms=%d", len(data), MINERU_MAX_ZIP_BYTES, elapsed_ms(started_at))
        raise HTTPException(status_code=413, detail=f"mineru result zip too large (max {MINERU_MAX_ZIP_BYTES} bytes)")
    text = extract_full_md_from_zip(data)
    if not text.strip():
        logger.warning("[py-extractor] mineru_markdown_empty duration_ms=%d", elapsed_ms(started_at))
        raise HTTPException(status_code=502, detail="mineru returned empty markdown")
    logger.info("[py-extractor] mineru_markdown_success chars=%d bytes=%d duration_ms=%d", len(text), len(data), elapsed_ms(started_at))
    return text


def extract_full_md_from_zip(data: bytes) -> str:
    try:
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            candidates = [name for name in archive.namelist() if name == "full.md" or name.endswith("/full.md")]
            if not candidates:
                raise HTTPException(status_code=502, detail="mineru zip missing full.md")
            with archive.open(candidates[0]) as full_md:
                raw = full_md.read(MAX_OUTPUT_BYTES + 1)
    except zipfile.BadZipFile as exc:
        raise HTTPException(status_code=502, detail="mineru returned invalid zip") from exc
    if len(raw) > MAX_OUTPUT_BYTES:
        raise HTTPException(status_code=413, detail=f"mineru markdown too large (max {MAX_OUTPUT_BYTES} bytes)")
    return normalize_markdown(raw.decode("utf-8", errors="replace"))


def parser_for(ext: str, content_type: str) -> Callable[[bytes, str], ExtractedDocument] | None:
    lower_type = content_type.lower()
    if ext == ".pdf" or lower_type == "application/pdf":
        return extract_pdf
    if ext == ".docx" or lower_type.endswith("wordprocessingml.document"):
        return extract_docx
    if ext == ".pptx" or lower_type.endswith("presentationml.presentation"):
        return extract_pptx
    if ext == ".xlsx" or lower_type.endswith("spreadsheetml.sheet"):
        return extract_xlsx
    if ext == ".csv" or lower_type in {"text/csv", "application/csv"}:
        return extract_csv
    return None


def extract_pdf(data: bytes, filename: str) -> ExtractedDocument:
    oxide_result = try_pdf_oxide(data, filename)
    if oxide_result and oxide_result.text.strip():
        return oxide_result
    fallback = extract_pdf_with_pdfplumber(data)
    if oxide_result:
        fallback.warnings.extend(oxide_result.warnings)
    fallback.warnings.append("pdf_oxide_unavailable_or_empty; used pdfplumber fallback")
    return fallback


def try_pdf_oxide(data: bytes, filename: str) -> ExtractedDocument | None:
    pdf_oxide = import_optional("pdf_oxide")
    if pdf_oxide is None:
        return ExtractedDocument(text="", parser="pdfplumber", warnings=["pdf_oxide package import failed"])

    with tempfile.NamedTemporaryFile(suffix=".pdf") as tmp:
        tmp.write(data)
        tmp.flush()
        candidates = [
            ("extract_markdown", lambda mod: mod.extract_markdown(tmp.name)),
            ("extract_text", lambda mod: mod.extract_text(tmp.name)),
            ("to_markdown", lambda mod: mod.to_markdown(tmp.name)),
        ]
        warnings: list[str] = []
        for name, call in candidates:
            if not hasattr(pdf_oxide, name):
                continue
            try:
                value = call(pdf_oxide)
            except Exception as exc:
                warnings.append(f"pdf_oxide.{name} failed: {exc}")
                continue
            text = stringify_pdf_oxide_result(value)
            if text.strip():
                return ExtractedDocument(text=text, parser=f"pdf_oxide.{name}", warnings=warnings)
        warnings.append("pdf_oxide has no known extraction entrypoint")
        return ExtractedDocument(text="", parser="pdfplumber", warnings=warnings)


def stringify_pdf_oxide_result(value: object) -> str:
    if value is None:
        return ""
    if isinstance(value, str):
        return value
    if isinstance(value, bytes):
        return value.decode("utf-8", errors="ignore")
    if isinstance(value, dict):
        for key in ("markdown", "text", "content"):
            raw = value.get(key)
            if isinstance(raw, str):
                return raw
        if "pages" in value and isinstance(value["pages"], list):
            return "\n\n".join(stringify_pdf_oxide_result(page) for page in value["pages"])
    if isinstance(value, list):
        return "\n\n".join(stringify_pdf_oxide_result(item) for item in value)
    return str(value)


def extract_pdf_with_pdfplumber(data: bytes) -> ExtractedDocument:
    pages: list[str] = []
    table_count = 0
    with pdfplumber.open(io.BytesIO(data)) as pdf:
        for index, page in enumerate(pdf.pages, start=1):
            chunks: list[str] = []
            text = page.extract_text(x_tolerance=1, y_tolerance=3) or ""
            if text.strip():
                chunks.append(text.strip())
            tables = page.extract_tables() or []
            for table in tables:
                md = markdown_table.serialize(table)
                if md:
                    chunks.append(md)
                    table_count += 1
            if chunks:
                pages.append(f"## Page {index}\n\n" + "\n\n".join(chunks))
        page_count = len(pdf.pages)
    return ExtractedDocument(
        text="\n\n".join(pages),
        parser="pdfplumber",
        page_count=page_count,
        paragraph_count=sum(count_paragraphs(page) for page in pages),
        table_count=table_count,
    )


def extract_docx(data: bytes, filename: str) -> ExtractedDocument:
    resource_limits.validate_office_archive(data)
    content = docx_content.extract(data)
    return ExtractedDocument(
        text=content.text,
        parser="python-docx",
        paragraph_count=content.paragraph_count,
        table_count=content.table_count,
    )


def extract_pptx(data: bytes, filename: str) -> ExtractedDocument:
    resource_limits.validate_office_archive(data)
    content = pptx_content.extract(data)
    return ExtractedDocument(
        text=content.text,
        parser="python-pptx",
        page_count=content.slide_count,
        paragraph_count=count_paragraphs(content.text),
        table_count=content.table_count,
        warnings=content.warnings,
    )


def extract_xlsx(data: bytes, filename: str) -> ExtractedDocument:
    resource_limits.validate_office_archive(data)
    content = xlsx_content.extract(data)
    return ExtractedDocument(
        text=content.text,
        parser="openpyxl",
        table_count=content.table_count,
        paragraph_count=content.paragraph_count,
    )


def extract_csv(data: bytes, filename: str) -> ExtractedDocument:
    text = decode_text(data)
    rows = resource_limits.read_bounded_csv(text, MAX_OUTPUT_BYTES)
    return ExtractedDocument(
        text=markdown_table.serialize(rows),
        parser="python-csv",
        table_count=1 if rows else 0,
        paragraph_count=len(rows),
    )


def normalize_markdown(text: str) -> str:
    text = text.replace("\r\n", "\n").replace("\r", "\n")
    lines = [line.rstrip() for line in text.split("\n")]
    out: list[str] = []
    blank = False
    for line in lines:
        if not line.strip():
            if not blank:
                out.append("")
            blank = True
            continue
        out.append(line)
        blank = False
    return "\n".join(out).strip()


def count_paragraphs(text: str) -> int:
    return len([part for part in text.split("\n\n") if part.strip()])


def estimate_tokens(text: str) -> int:
    cjk = sum(1 for ch in text if "\u4e00" <= ch <= "\u9fff")
    non_cjk = max(len(text) - cjk, 0)
    return max(1, cjk + non_cjk // 4) if text.strip() else 0


def decode_text(data: bytes) -> str:
    for encoding in ("utf-8-sig", "utf-8", "gb18030", "latin-1"):
        try:
            return data.decode(encoding)
        except UnicodeDecodeError:
            continue
    return data.decode("utf-8", errors="replace")


def import_optional(name: str) -> object | None:
    try:
        return importlib.import_module(name)
    except Exception:
        return None
