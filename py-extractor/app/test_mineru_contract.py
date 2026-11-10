"""Synthetic OCR contract checks; no external OCR calls or real documents."""

import io
import json
import threading
import unittest
import zipfile
from unittest import mock
from urllib import error as urlerror

from fastapi import HTTPException, UploadFile

from app import main, mineru_transport


def result_zip(text: str) -> bytes:
    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, "w") as archive:
        archive.writestr("result/full.md", text)
        archive.writestr("result/middle.json", "{}")
    return buffer.getvalue()


class MinerUContractTest(unittest.IsolatedAsyncioTestCase):
    async def test_start_and_completed_poll_keep_route_contract(self):
        event_loop_thread = threading.get_ident()
        requests = []
        markdown_zip = result_zip("# Title\r\n\r\n\r\nBody  \r\n")

        def upstream(request, timeout):
            self.assertNotEqual(threading.get_ident(), event_loop_thread)
            if isinstance(request, str):
                self.assertEqual(request, "https://result.example/fixture.zip")
                self.assertEqual(timeout, 60)
                return io.BytesIO(markdown_zip)
            requests.append(request)
            self.assertEqual(request.get_header("Authorization"), "Bearer fixture-token")
            self.assertEqual(timeout, 30)
            if request.get_method() == "POST":
                return io.BytesIO(json.dumps({"code": 0, "data": {
                    "batch_id": "fixture-task", "file_urls": ["https://upload.example/fixture.pdf"],
                }}).encode())
            return io.BytesIO(json.dumps({"code": 0, "data": {"extract_result": [{
                "state": "done", "full_zip_url": "https://result.example/fixture.zip",
            }]}}).encode())

        with (
            mock.patch.object(mineru_transport.urlrequest, "urlopen", side_effect=upstream),
            mock.patch.object(main, "count_pdf_pages", return_value=2),
            mock.patch.object(mineru_transport, "put_file") as upload,
        ):
            started = await main.start_mineru_ocr(
                UploadFile(io.BytesIO(b"synthetic-pdf"), filename="fixture.pdf"),
                filename="fixture.pdf", base_url="https://ocr.example/", api_key=" fixture-token ",
            )
            self.assertEqual(started, {"task_id": "fixture-task", "state": "ocr_running", "page_count": 2})
            upload.assert_called_once_with("https://upload.example/fixture.pdf", b"synthetic-pdf")
            completed = await main.get_mineru_ocr_task(
                "fixture-task", base_url="https://ocr.example/", mineru_token=" fixture-token ",
            )
        self.assertEqual(completed, {"state": "ready", "markdown": "# Title\n\nBody", "token_estimate": 3})
        self.assertEqual(requests[0].full_url, "https://ocr.example/api/v4/file-urls/batch")
        self.assertEqual(json.loads(requests[0].data), {
            "files": [{"name": "fixture.pdf", "is_ocr": True}], "model_version": "vlm",
            "language": "ch", "enable_table": True, "enable_formula": True,
        })
        self.assertEqual(requests[1].full_url, "https://ocr.example/api/v4/extract-results/batch/fixture-task")
        self.assertIsNone(requests[1].data)

    async def test_poll_nonterminal_and_failed_states(self):
        for state, expected in [
            ("running", {"state": "ocr_running", "mineru_state": "running"}),
            ("queued", {"state": "ocr_pending", "mineru_state": "queued"}),
            ("failed", {"state": "failed", "error": "fixture error", "error_type": "fixture_code"}),
        ]:
            with self.subTest(state=state), mock.patch.object(mineru_transport.urlrequest, "urlopen", return_value=io.BytesIO(json.dumps({
                "code": "0", "data": {"extract_result": [{"state": state, "err_msg": "fixture error", "err_code": "fixture_code"}]},
            }).encode())):
                result = await main.get_mineru_ocr_task("fixture-task", "https://ocr.example", "fixture-token")
                self.assertEqual(result, expected)

    async def test_missing_token_fails_before_network(self):
        with mock.patch.object(mineru_transport.urlrequest, "urlopen") as upstream:
            with self.assertRaises(HTTPException) as failure:
                await main.get_mineru_ocr_task("fixture-task", "https://ocr.example", "")
            self.assertEqual(failure.exception.status_code, 400)
            upstream.assert_not_called()


class MinerUFailureContractTest(unittest.TestCase):
    def test_json_response_errors_keep_classification(self):
        for payload, detail in [
            (b"invalid", "mineru returned invalid JSON"),
            (b"[]", "mineru returned invalid response"),
            (b'{"code": 1, "msg": "fixture rejection"}', "mineru rejected request: fixture rejection"),
        ]:
            with self.subTest(payload=payload), mock.patch.object(mineru_transport.urlrequest, "urlopen", return_value=io.BytesIO(payload)):
                with self.assertRaises(HTTPException) as failure:
                    mineru_transport.json_request("GET", "https://ocr.example/fixture", None, "fixture-token")
                self.assertEqual(failure.exception.status_code, 502)
                self.assertEqual(failure.exception.detail, detail)
        for error, status in [(urlerror.URLError("fixture unavailable"), 502), (urlerror.HTTPError("https://ocr.example", 429, "limited", {}, io.BytesIO(b"limit")), 429)]:
            with self.subTest(status=status), mock.patch.object(mineru_transport.urlrequest, "urlopen", side_effect=error):
                with self.assertRaises(HTTPException) as failure:
                    mineru_transport.json_request("GET", "https://ocr.example/fixture", None, "fixture-token")
                self.assertEqual(failure.exception.status_code, status)

    def test_archive_limits_and_empty_content(self):
        with mock.patch.object(mineru_transport, "MINERU_MAX_ZIP_BYTES", 4), mock.patch.object(mineru_transport.urlrequest, "urlopen", return_value=io.BytesIO(b"12345")):
            with self.assertRaises(HTTPException) as failure:
                main.mineru_download_full_md("https://result.example/fixture.zip")
            self.assertEqual(failure.exception.status_code, 413)
        with mock.patch.object(main, "MAX_OUTPUT_BYTES", 4):
            with self.assertRaises(HTTPException) as failure:
                main.extract_full_md_from_zip(result_zip("12345"))
            self.assertEqual(failure.exception.status_code, 413)
        with mock.patch.object(mineru_transport.urlrequest, "urlopen", return_value=io.BytesIO(result_zip(" \r\n \r\n"))):
            with self.assertRaises(HTTPException) as failure:
                main.mineru_download_full_md("https://result.example/fixture.zip")
            self.assertEqual(failure.exception.detail, "mineru returned empty markdown")


if __name__ == "__main__":
    unittest.main()
