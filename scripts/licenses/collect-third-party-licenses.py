#!/usr/bin/env python3
"""Build and verify the third-party license archive shipped in EffChat images.

The archive is derived from the dependencies that each image actually installs
or compiles. A dependency without distributable license material fails the
build unless a version-pinned fallback is recorded beside this script.
"""

from __future__ import annotations

import argparse
import fnmatch
import hashlib
import importlib.metadata
import json
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import sys
from typing import Any, Iterable


LICENSE_PREFIXES = ("license", "licence", "copying", "notice", "copyright")
SCRIPT_DIR = Path(__file__).resolve().parent
FALLBACKS_DIR = SCRIPT_DIR / "fallbacks"


def fail(message: str) -> None:
    raise RuntimeError(message)


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def safe_package_dir(name: str, version: str) -> str:
    readable = re.sub(r"[^A-Za-z0-9._-]+", "_", f"{name}@{version}").strip("._")
    suffix = hashlib.sha256(f"{name}@{version}".encode()).hexdigest()[:10]
    return f"{readable[:90]}-{suffix}"


def is_license_name(name: str) -> bool:
    lowered = name.lower()
    return bool(
        any(lowered == prefix or re.match(rf"^{prefix}[._-]", lowered) for prefix in LICENSE_PREFIXES)
        or re.match(r"^third[._-]party[._-]licenses?([._-].*)?$", lowered)
    )


def root_license_files(root: Path) -> list[Path]:
    if not root.is_dir():
        return []
    return sorted(path for path in root.iterdir() if path.is_file() and is_license_name(path.name))


def load_fallbacks() -> list[dict[str, Any]]:
    payload = json.loads((SCRIPT_DIR / "fallbacks.json").read_text(encoding="utf-8"))
    entries = payload.get("fallbacks")
    if not isinstance(entries, list):
        fail("fallbacks.json must contain a fallbacks array")
    return entries


def fallback_for(component: str, name: str, version: str) -> dict[str, Any] | None:
    matches = [
        entry
        for entry in load_fallbacks()
        if entry.get("component") == component
        and entry.get("version") == version
        and fnmatch.fnmatchcase(name, str(entry.get("name", "")))
    ]
    if len(matches) > 1:
        fail(f"multiple license fallbacks match {component} {name}@{version}")
    return matches[0] if matches else None


def copy_material(
    output: Path,
    package_dir: str,
    sources: Iterable[tuple[Path, str]],
) -> list[dict[str, str]]:
    records: list[dict[str, str]] = []
    used: set[str] = set()
    for index, (source, relative_name) in enumerate(sources, start=1):
        if not source.is_file() or source.stat().st_size == 0:
            fail(f"license material is missing or empty: {source}")
        normalized = PurePosixPath(relative_name)
        if normalized.is_absolute() or ".." in normalized.parts:
            fail(f"unsafe license archive path: {relative_name}")
        candidate = "__".join(part for part in normalized.parts if part not in ("", "."))
        if not candidate:
            candidate = f"license-{index}"
        while candidate in used:
            candidate = f"{index}-{candidate}"
        used.add(candidate)
        destination = output / "packages" / package_dir / candidate
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, destination)
        records.append(
            {
                "path": destination.relative_to(output).as_posix(),
                "sha256": sha256(destination),
            }
        )
    return records


def fallback_material(entry: dict[str, Any]) -> list[tuple[Path, str]]:
    files = entry.get("files")
    if not isinstance(files, list) or not files:
        fail("license fallback must declare at least one file")
    material: list[tuple[Path, str]] = []
    for relative in files:
        path = (FALLBACKS_DIR / str(relative)).resolve()
        if FALLBACKS_DIR.resolve() not in path.parents:
            fail(f"fallback escapes the controlled directory: {relative}")
        material.append((path, Path(str(relative)).name))
    return material


def package_record(
    *,
    component: str,
    name: str,
    version: str,
    source: str,
    declared_license: str | None,
    discovered: list[tuple[Path, str]],
    output: Path,
    metadata: dict[str, Any] | None = None,
) -> dict[str, Any]:
    fallback = None
    material = discovered
    if not material:
        fallback = fallback_for(component, name, version)
        if fallback is None:
            fail(f"no license material or approved fallback for {component} {name}@{version}")
        material = fallback_material(fallback)

    record: dict[str, Any] = {
        "name": name,
        "version": version,
        "source": source,
        "files": copy_material(output, safe_package_dir(name, version), material),
    }
    if declared_license:
        record["declared_license"] = declared_license
    if metadata:
        record["metadata"] = metadata
    if fallback:
        record["fallback"] = {
            "reason": fallback["reason"],
            "source": fallback["source"],
        }
    return record


def decode_json_stream(payload: str) -> Iterable[dict[str, Any]]:
    decoder = json.JSONDecoder()
    offset = 0
    while offset < len(payload):
        while offset < len(payload) and payload[offset].isspace():
            offset += 1
        if offset >= len(payload):
            break
        item, offset = decoder.raw_decode(payload, offset)
        yield item


def collect_backend(root: Path, output: Path) -> tuple[str, list[dict[str, Any]]]:
    result = subprocess.run(
        ["go", "list", "-mod=readonly", "-deps", "-json", "./cmd/server"],
        cwd=root,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
    )
    modules: dict[tuple[str, str], dict[str, Any]] = {}
    for package in decode_json_stream(result.stdout):
        module = package.get("Module")
        if not module or module.get("Main") or package.get("Standard"):
            continue
        effective = module.get("Replace") or module
        version = module.get("Version") or effective.get("Version") or "local"
        modules[(module["Path"], version)] = effective

    records = []
    for (name, version), effective in sorted(modules.items()):
        module_root = Path(str(effective.get("Dir", "")))
        discovered = [(path, path.name) for path in root_license_files(module_root)]
        records.append(
            package_record(
                component="backend",
                name=name,
                version=version,
                source=str(effective.get("Path") or name),
                declared_license=None,
                discovered=discovered,
                output=output,
            )
        )
    return "go list -mod=readonly -deps -json ./cmd/server", records


def walk_npm_dependencies(node: dict[str, Any], packages: dict[tuple[str, str], dict[str, Any]]) -> None:
    name = node.get("name")
    version = node.get("version")
    path = node.get("path")
    if name and version and path:
        packages[(name, version)] = node
    for dependency in (node.get("dependencies") or {}).values():
        if isinstance(dependency, dict):
            walk_npm_dependencies(dependency, packages)


def collect_frontend(root: Path, output: Path) -> tuple[str, list[dict[str, Any]]]:
    production_result = subprocess.run(
        ["npm", "ls", "--omit=dev", "--all", "--json", "--long"],
        cwd=root,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
    )
    all_result = subprocess.run(
        ["npm", "ls", "--all", "--json", "--long"],
        cwd=root,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
    )
    packages: dict[tuple[str, str], dict[str, Any]] = {}
    for dependency in (json.loads(production_result.stdout).get("dependencies") or {}).values():
        if isinstance(dependency, dict):
            walk_npm_dependencies(dependency, packages)

    # The generated service worker and registration helper contain Workbox,
    # Vite, and vite-plugin-pwa runtime code even though the build packages are
    # devDependencies. Include only those known bundled roots rather than the
    # entire development toolchain.
    bundled_build_packages: dict[tuple[str, str], dict[str, Any]] = {}
    for dependency in (json.loads(all_result.stdout).get("dependencies") or {}).values():
        if isinstance(dependency, dict):
            walk_npm_dependencies(dependency, bundled_build_packages)
    for identity, node in bundled_build_packages.items():
        name = identity[0]
        if name in {"vite", "vite-plugin-pwa"} or name.startswith("workbox-"):
            packages[identity] = node

    records = []
    for (name, version), node in sorted(packages.items()):
        package_root = Path(node["path"])
        package_json = json.loads((package_root / "package.json").read_text(encoding="utf-8"))
        repository = package_json.get("repository")
        if isinstance(repository, dict):
            repository = repository.get("url")
        author = package_json.get("author")
        if isinstance(author, dict):
            author = " ".join(str(author.get(key, "")) for key in ("name", "email", "url")).strip()
        metadata = {key: value for key, value in {"author": author}.items() if value}
        discovered = [(path, path.name) for path in root_license_files(package_root)]
        records.append(
            package_record(
                component="frontend",
                name=name,
                version=version,
                source=str(repository or package_json.get("homepage") or f"npm:{name}@{version}"),
                declared_license=str(package_json.get("license") or "") or None,
                discovered=discovered,
                output=output,
                metadata=metadata,
            )
        )
    return "npm production tree plus bundled Vite/PWA runtime packages", records


def normalize_python_name(name: str) -> str:
    return re.sub(r"[-_.]+", "-", name).lower()


def locked_python_requirements(lock: Path) -> dict[str, tuple[str, str]]:
    requirements: dict[str, tuple[str, str]] = {}
    for line in lock.read_text(encoding="utf-8").splitlines():
        match = re.match(r"^([A-Za-z0-9_.-]+)==([^\s\\]+)", line)
        if match:
            requirements[normalize_python_name(match.group(1))] = (match.group(1), match.group(2))
    if not requirements:
        fail(f"no pinned Python requirements found in {lock}")
    return requirements


def python_license_files(distribution: importlib.metadata.Distribution) -> list[tuple[Path, str]]:
    all_installed: dict[str, Path] = {}
    selected: dict[str, Path] = {}
    for entry in distribution.files or []:
        relative = PurePosixPath(str(entry))
        path = Path(distribution.locate_file(entry))
        if path.is_file():
            all_installed[relative.as_posix()] = path
            lowered_parts = [part.lower() for part in relative.parts]
            if "licenses" in lowered_parts or is_license_name(relative.name):
                selected[relative.as_posix()] = path

    for declared in distribution.metadata.get_all("License-File") or []:
        matches = [
            (relative, path)
            for relative, path in all_installed.items()
            if relative.endswith(str(declared))
        ]
        if not matches:
            fail(
                f"{distribution.metadata.get('Name')}@{distribution.version} declares missing "
                f"License-File {declared}"
            )
        selected.update(matches)
    return [(path, relative) for relative, path in sorted(selected.items())]


def python_source(distribution: importlib.metadata.Distribution, name: str, version: str) -> str:
    for value in distribution.metadata.get_all("Project-URL") or []:
        if "," in value:
            label, url = value.split(",", 1)
            if label.strip().lower() in {"source", "repository", "homepage"}:
                return url.strip()
    return str(distribution.metadata.get("Home-page") or f"pypi:{name}@{version}")


def collect_python(root: Path, output: Path) -> tuple[str, list[dict[str, Any]]]:
    locked = locked_python_requirements(root / "requirements.lock")
    installed = {
        normalize_python_name(distribution.metadata["Name"]): distribution
        for distribution in importlib.metadata.distributions()
        if distribution.metadata.get("Name")
    }
    records = []
    for normalized, (locked_name, version) in sorted(locked.items()):
        distribution = installed.get(normalized)
        if distribution is None:
            fail(f"locked Python distribution is not installed: {locked_name}=={version}")
        if distribution.version != version:
            fail(
                f"Python distribution version mismatch for {locked_name}: "
                f"lock={version}, installed={distribution.version}"
            )
        records.append(
            package_record(
                component="python",
                name=str(distribution.metadata["Name"]),
                version=version,
                source=python_source(distribution, locked_name, version),
                declared_license=str(distribution.metadata.get("License-Expression") or distribution.metadata.get("License") or "") or None,
                discovered=python_license_files(distribution),
                output=output,
            )
        )
    return "requirements.lock plus installed importlib.metadata", records


def write_archive(component: str, root: Path, output: Path) -> None:
    if output.exists():
        shutil.rmtree(output)
    output.mkdir(parents=True)
    collectors = {
        "backend": collect_backend,
        "frontend": collect_frontend,
        "python": collect_python,
    }
    generated_from, packages = collectors[component](root.resolve(), output.resolve())
    if not packages:
        fail(f"no {component} dependencies were collected")
    manifest = {
        "schema": 1,
        "component": component,
        "generated_from": generated_from,
        "packages": packages,
    }
    (output / "manifest.json").write_text(
        json.dumps(manifest, indent=2, sort_keys=True, ensure_ascii=False) + "\n",
        encoding="utf-8",
    )


def verify_archive(archive: Path, expected_component: str | None) -> None:
    manifest_path = archive / "manifest.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    if manifest.get("schema") != 1:
        fail("unsupported third-party license manifest schema")
    if expected_component and manifest.get("component") != expected_component:
        fail(f"expected {expected_component} archive, found {manifest.get('component')}")
    packages = manifest.get("packages")
    if not isinstance(packages, list) or not packages:
        fail("third-party license manifest has no packages")

    expected_files = {"manifest.json"}
    identities: set[tuple[str, str]] = set()
    for package in packages:
        identity = (str(package.get("name", "")), str(package.get("version", "")))
        if not all(identity) or identity in identities:
            fail(f"invalid or duplicate package identity: {identity}")
        identities.add(identity)
        files = package.get("files")
        if not isinstance(files, list) or not files:
            fail(f"package has no archived license files: {identity[0]}@{identity[1]}")
        for item in files:
            relative = PurePosixPath(str(item.get("path", "")))
            if relative.is_absolute() or ".." in relative.parts:
                fail(f"unsafe path in manifest: {relative}")
            path = archive / relative
            if not path.is_file() or path.stat().st_size == 0:
                fail(f"missing or empty archived license: {relative}")
            if sha256(path) != item.get("sha256"):
                fail(f"license checksum mismatch: {relative}")
            expected_files.add(relative.as_posix())

    actual_files = {
        path.relative_to(archive).as_posix()
        for path in archive.rglob("*")
        if path.is_file()
    }
    if actual_files != expected_files:
        fail(
            "archive contains files not represented by the manifest: "
            f"missing={sorted(expected_files - actual_files)}, extra={sorted(actual_files - expected_files)}"
        )


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    subparsers = parser.add_subparsers(dest="command", required=True)
    collect = subparsers.add_parser("collect")
    collect.add_argument("component", choices=("backend", "frontend", "python"))
    collect.add_argument("--root", type=Path, required=True)
    collect.add_argument("--output", type=Path, required=True)
    verify = subparsers.add_parser("verify")
    verify.add_argument("--archive", type=Path, required=True)
    verify.add_argument("--component", choices=("backend", "frontend", "python"))
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    try:
        if args.command == "collect":
            write_archive(args.component, args.root, args.output)
            verify_archive(args.output, args.component)
        else:
            verify_archive(args.archive.resolve(), args.component)
    except (OSError, RuntimeError, subprocess.CalledProcessError, json.JSONDecodeError) as error:
        print(f"third-party license archive error: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
