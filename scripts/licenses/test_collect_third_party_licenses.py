#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest


SCRIPT = Path(__file__).with_name("collect-third-party-licenses.py")
SPEC = importlib.util.spec_from_file_location("license_collector", SCRIPT)
assert SPEC and SPEC.loader
collector = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(collector)


class LicenseArchiveTests(unittest.TestCase):
    def test_fallbacks_are_version_pinned_and_material_exists(self) -> None:
        for entry in collector.load_fallbacks():
            self.assertTrue(entry["component"])
            self.assertTrue(entry["name"])
            self.assertTrue(entry["version"])
            self.assertTrue(entry["source"])
            self.assertTrue(entry["reason"])
            for path, _ in collector.fallback_material(entry):
                self.assertGreater(path.stat().st_size, 0)

    def test_platform_fallback_matches_only_the_pinned_version(self) -> None:
        self.assertIsNotNone(
            collector.fallback_for("frontend", "@rolldown/binding-linux-x64-gnu", "1.1.5")
        )
        self.assertIsNone(
            collector.fallback_for("frontend", "@rolldown/binding-linux-x64-gnu", "1.1.6")
        )

    def test_verify_rejects_tampered_license_text(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            archive = Path(directory)
            license_path = archive / "packages" / "example" / "LICENSE"
            license_path.parent.mkdir(parents=True)
            license_path.write_text("original license\n", encoding="utf-8")
            manifest = {
                "schema": 1,
                "component": "backend",
                "generated_from": "test fixture",
                "packages": [
                    {
                        "name": "example.org/module",
                        "version": "v1.0.0",
                        "source": "https://example.org/module",
                        "files": [
                            {
                                "path": "packages/example/LICENSE",
                                "sha256": hashlib.sha256(license_path.read_bytes()).hexdigest(),
                            }
                        ],
                    }
                ],
            }
            (archive / "manifest.json").write_text(
                json.dumps(manifest), encoding="utf-8"
            )
            collector.verify_archive(archive, "backend")
            license_path.write_text("tampered\n", encoding="utf-8")
            with self.assertRaisesRegex(RuntimeError, "checksum mismatch"):
                collector.verify_archive(archive, "backend")


if __name__ == "__main__":
    unittest.main()
