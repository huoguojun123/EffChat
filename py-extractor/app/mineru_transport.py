"""Blocking transport for the administrator-configured MinerU upstream.

Routes must call this module from their worker thread. Signed upload/result URLs
come from that trusted upstream, not from an arbitrary public URL fetch API.
Task-state interpretation and document decoding/normalization stay in main.
"""

import http.client
import json
import logging
import os
import time
from urllib import error as urlerror
from urllib import request as urlrequest
from urllib.parse import urlsplit

from fastapi import HTTPException

logger = logging.getLogger("uvicorn.error")
MINERU_MAX_ZIP_BYTES = int(os.getenv("MINERU_MAX_ZIP_BYTES", str(100 * 1024 * 1024)))
MINERU_UPLOAD_TIMEOUT_SECONDS = int(os.getenv("MINERU_UPLOAD_TIMEOUT_SECONDS", "300"))
MINERU_DEFAULT_BASE_URL = "https://mineru.net"


def elapsed_ms(started_at: float) -> int:
    return int((time.perf_counter() - started_at) * 1000)


def normalize_base_url(value: str) -> str:
    base = (value or MINERU_DEFAULT_BASE_URL).strip().rstrip("/")
    return base or MINERU_DEFAULT_BASE_URL


def json_request(method: str, url: str, payload: dict[str, object] | None, api_key: str) -> dict[str, object]:
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


def put_file(file_url: str, data: bytes) -> None:
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
        # Do not add Authorization or Content-Type: this is a signed object-store
        # URL, not the JSON API. Extra headers can invalidate the upload signature.
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


def download_result_zip(full_zip_url: str) -> bytes:
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
    return data
