import unittest
import io
import importlib.util
import sys
import types
import zipfile

if importlib.util.find_spec("pdfplumber") is None:
    pdfplumber = types.ModuleType("pdfplumber")
    pdfplumber.open = None
    sys.modules["pdfplumber"] = pdfplumber
if importlib.util.find_spec("docx") is None:
    docx = types.ModuleType("docx")
    docx.Document = object
    sys.modules["docx"] = docx
if importlib.util.find_spec("openpyxl") is None:
    openpyxl = types.ModuleType("openpyxl")
    openpyxl.load_workbook = None
    sys.modules["openpyxl"] = openpyxl
if importlib.util.find_spec("pptx") is None:
    pptx = types.ModuleType("pptx")
    pptx.Presentation = object
    sys.modules["pptx"] = pptx
if importlib.util.find_spec("fastapi") is None:
    fastapi = types.ModuleType("fastapi")

    class FastAPI:
        def __init__(self, *args, **kwargs):
            pass

        def get(self, *args, **kwargs):
            return lambda fn: fn

        def post(self, *args, **kwargs):
            return lambda fn: fn

    class HTTPException(Exception):
        def __init__(self, status_code: int, detail: str = ""):
            super().__init__(detail)
            self.status_code = status_code
            self.detail = detail

    def marker(default=None, *args, **kwargs):
        return default

    fastapi.FastAPI = FastAPI
    fastapi.File = marker
    fastapi.Form = marker
    fastapi.Header = marker
    fastapi.HTTPException = HTTPException
    fastapi.Query = marker
    fastapi.UploadFile = object
    sys.modules["fastapi"] = fastapi

from app import main


class MinerUHelpersTest(unittest.TestCase):
    def test_normalize_mineru_base_url_default(self):
        self.assertEqual(main.normalize_mineru_base_url(""), main.MINERU_DEFAULT_BASE_URL)

    def test_normalize_mineru_base_url_trims_slash(self):
        self.assertEqual(main.normalize_mineru_base_url("https://mineru.net/"), "https://mineru.net")

    def test_extract_full_md_from_zip(self):
        buf = io.BytesIO()
        with zipfile.ZipFile(buf, "w") as zf:
            zf.writestr("result/full.md", "# Title\n\nBody")
            zf.writestr("result/middle.json", "{}")
        self.assertEqual(main.extract_full_md_from_zip(buf.getvalue()), "# Title\n\nBody")

    def test_mineru_put_file_does_not_send_content_type(self):
        calls = []

        class FakeResponse:
            status = 200

            def read(self, size=-1):
                return b""

        class FakeConnection:
            def __init__(self, host, timeout):
                self.host = host
                self.timeout = timeout

            def request(self, method, path, body=None, headers=None):
                calls.append({"method": method, "path": path, "body": body, "headers": headers or {}, "timeout": self.timeout})

            def getresponse(self):
                return FakeResponse()

            def close(self):
                pass

        original = main.http.client.HTTPSConnection
        main.http.client.HTTPSConnection = FakeConnection
        try:
            main.mineru_put_file("https://upload.example/path/file.pdf?signature=redacted", b"pdf")
        finally:
            main.http.client.HTTPSConnection = original

        self.assertEqual(calls[0]["headers"], {"Content-Length": "3"})
        self.assertEqual(calls[0]["method"], "PUT")
        self.assertEqual(calls[0]["path"], "/path/file.pdf?signature=redacted")
        self.assertEqual(calls[0]["body"], b"pdf")
        self.assertEqual(calls[0].get("timeout"), main.MINERU_UPLOAD_TIMEOUT_SECONDS)


if __name__ == "__main__":
    unittest.main()
