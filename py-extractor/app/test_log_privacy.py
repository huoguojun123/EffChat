"""Log boundary tests use fictional sensitive markers, never real documents."""

import io
import time
import unittest
from unittest import mock
from urllib import error as urlerror

from fastapi import HTTPException, UploadFile

from app import main, mineru_transport

MARKER = "private-fixture-do-not-log"


class ExtractorLogPrivacyTest(unittest.IsolatedAsyncioTestCase):
    async def test_success_and_parser_failures_do_not_log_document_identity(self):
        for result in [main.ExtractedDocument("synthetic text", "python-docx"),
                       ValueError(f"/private/{MARKER}.docx"),
                       HTTPException(422, f"upstream {MARKER}")]:
            with self.subTest(result=type(result).__name__):
                parser = mock.AsyncMock(side_effect=result if isinstance(result, Exception) else None,
                                   return_value=result)
                with mock.patch.object(main, "run_local_parser", parser), self.assertLogs(main.logger, "INFO") as captured:
                    try:
                        await main.extract(UploadFile(io.BytesIO(b"synthetic"), filename=f"{MARKER}.docx"),
                                           filename=f"{MARKER}.docx", content_type="application/vnd.openxmlformats-officedocument.wordprocessingml.document")
                    except HTTPException:
                        pass
                logs = "\n".join(captured.output)
                self.assertNotIn(MARKER, logs)
                self.assertNotIn("Traceback", logs)
                self.assertIn("duration_ms=", logs)

    async def test_mineru_start_keeps_name_in_request_but_not_logs(self):
        with (mock.patch.object(main, "count_pdf_pages", return_value=1),
              mock.patch.object(mineru_transport, "json_request", return_value={"data": {"batch_id": "fixture-task", "file_urls": ["https://upload.example/fixture"]}}) as submit,
              mock.patch.object(mineru_transport, "put_file"), self.assertLogs(main.logger, "INFO") as captured):
            main.start_mineru_ocr_bytes(b"synthetic", f"{MARKER}.pdf", "https://ocr.example", "fixture-key", time.perf_counter())
        self.assertNotIn(MARKER, "\n".join(captured.output))
        self.assertEqual(submit.call_args.args[2]["files"][0]["name"], f"{MARKER}.pdf")

    async def test_transport_failure_does_not_log_signed_url_or_reason(self):
        with (mock.patch.object(mineru_transport.urlrequest, "urlopen", side_effect=urlerror.URLError(f"https://private.example/{MARKER}?token=fixture")),
              self.assertLogs(main.logger, "WARNING") as captured, self.assertRaises(HTTPException)):
            mineru_transport.download_result_zip("https://result.example/fixture.zip")
        self.assertNotIn(MARKER, "\n".join(captured.output))
        self.assertIn("duration_ms=", "\n".join(captured.output))
