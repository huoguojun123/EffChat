import unittest
from unittest import mock

from app import mineru_transport


class MinerUHelpersTest(unittest.TestCase):
    def test_normalize_mineru_base_url_default(self):
        self.assertEqual(mineru_transport.normalize_base_url(""), mineru_transport.MINERU_DEFAULT_BASE_URL)

    def test_normalize_mineru_base_url_trims_slash(self):
        self.assertEqual(mineru_transport.normalize_base_url("https://mineru.net/"), "https://mineru.net")

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

        with mock.patch.object(mineru_transport.http.client, "HTTPSConnection", FakeConnection):
            mineru_transport.put_file("https://upload.example/path/file.pdf?signature=redacted", b"pdf")

        self.assertEqual(calls[0]["headers"], {"Content-Length": "3"})
        self.assertEqual(calls[0]["method"], "PUT")
        self.assertEqual(calls[0]["path"], "/path/file.pdf?signature=redacted")
        self.assertEqual(calls[0]["body"], b"pdf")
        self.assertEqual(calls[0].get("timeout"), mineru_transport.MINERU_UPLOAD_TIMEOUT_SECONDS)


if __name__ == "__main__":
    unittest.main()
