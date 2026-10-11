#!/usr/bin/env python3
"""Build all Windows roles, then package exactly those verified payloads.

Build first, validate the executables, commit the generated payloads, then run
the package command. Packaging rejects changed sources or mismatched binaries.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import zipfile


ROOT = Path(__file__).resolve().parents[1]
ROLES = {"admin": "Admin", "personnel": "Personel", "customer": "Musteri"}
PAYLOAD = ROOT / "v31/installer/payload"


def run(args, cwd=ROOT, env=None):
    subprocess.run(args, cwd=cwd, env=env, check=True)


def git(*args):
    return subprocess.check_output(["git", *args], cwd=ROOT).decode().strip()


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def source_digest():
    result = hashlib.sha256()
    for path in sorted((ROOT / "v31").rglob("*")):
        if path.is_file() and path.suffix != ".exe":
            result.update(path.relative_to(ROOT).as_posix().encode() + b"\0")
            result.update(path.read_bytes())
    return result.hexdigest()


def build_tag():
    tags = []
    for source, name in [("server/server_core.go", "centralBuild"),
                         ("client/health.go", "healthBuild"),
                         ("installer/health.go", "healthBuild")]:
        match = re.search(rf'{name}\s*=\s*"([^"]+)"',
                          (ROOT / "v31" / source).read_text())
        if not match:
            raise RuntimeError(f"Missing {name}: {source}")
        tags.append(match.group(1))
    if len(set(tags)) != 1:
        raise RuntimeError(f"Server, client and installer builds differ: {tags}")
    return tags[0]


def build(args):
    args.output.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, GOOS="windows", GOARCH="amd64", CGO_ENABLED="0")
    tag = build_tag()
    compiled = source_digest()

    def compile(module, target, extra=""):
        run([args.go, "build", "-trimpath", "-buildvcs=false", "-ldflags",
             "-s -w -H=windowsgui " + extra, "-o", str(target), "."],
            ROOT / "v31" / module, env)
        if target.read_bytes()[:2] != b"MZ":
            raise RuntimeError(f"Not a Windows executable: {target}")

    compile("server", PAYLOAD / "EBS_v3_Service.exe")
    for role, label in ROLES.items():
        compile("client", PAYLOAD / f"EBS_{label}.exe",
                f"-X main.clientMode={role}")
    payloads = {p.name: digest(p) for p in sorted(PAYLOAD.glob("*.exe"))}
    installers = {}
    for role, label in ROLES.items():
        target = args.output / f"EBS_{label}_Installer.exe"
        compile("installer", target, f"-X main.installerRole={role}")
        installers[target.name] = digest(target)
    manifest = {"build": tag, "compiled_source_sha256": compiled,
                "build_source_commit": git("rev-parse", "HEAD"),
                "go_version": subprocess.check_output([args.go, "version"]).decode().strip(),
                "payloads": payloads, "installers": installers}
    (args.output / "BUILD_MANIFEST.json").write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(manifest, ensure_ascii=False, indent=2))


def package(args):
    manifest = json.loads((args.output / "BUILD_MANIFEST.json").read_text())
    if manifest["build"] != build_tag() or manifest["compiled_source_sha256"] != source_digest():
        raise RuntimeError("Sources changed since the Windows executables were built")
    for name, expected in manifest["payloads"].items():
        if digest(PAYLOAD / name) != expected:
            raise RuntimeError(f"Payload changed since installer compilation: {name}")
    for name, expected in manifest["installers"].items():
        if digest(args.output / name) != expected:
            raise RuntimeError(f"Installer hash mismatch: {name}")
    if git("status", "--porcelain", "--untracked-files=no"):
        raise RuntimeError("Commit source and payload changes before packaging")
    manifest["source_commit"] = git("rev-parse", "HEAD")
    install = args.output / "install"
    install.mkdir(exist_ok=True)
    for name in manifest["installers"]:
        shutil.copy2(args.output / name, install / name)
    shutil.copy2(PAYLOAD / "GIRIS_BILGILERI.txt", install / "GIRIS_BILGILERI.txt")
    instructions = ROOT / "docs/WINDOWS_KURULUM.txt"
    if instructions.exists():
        shutil.copy2(instructions, install / "KURULUM_SIRASI.txt")
    (install / "BUILD_MANIFEST.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    (install / "SHA256SUMS.txt").write_text("".join(
        f"{sha}  {name}\n" for name, sha in manifest["installers"].items()))
    for file_name, base in [(f"EBS_KURULUM_PAKETI_V3_1_{args.date}.zip", install),
                            (f"EBS_KURULUM_DISI_DOSYALAR_V3_1_{args.date}.zip", ROOT)]:
        target = args.output / file_name
        with zipfile.ZipFile(target, "w", zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
            if base == install:
                for path in sorted(install.iterdir()):
                    if path.is_file():
                        archive.write(path, path.name)
            else:
                archive.writestr("BUILD_MANIFEST.json", json.dumps(manifest, ensure_ascii=False, indent=2))
                for name in git("ls-files").splitlines():
                    path = ROOT / name
                    if path.is_file() and path.suffix != ".zip":
                        archive.write(path, "EBS_SOURCE/" + name)
        with zipfile.ZipFile(target) as archive:
            if archive.testzip() is not None:
                raise RuntimeError(f"Invalid ZIP: {target}")
        print(f"{digest(target)}  {target}")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["build", "package"])
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--go", default="go")
    parser.add_argument("--date", default="20261011")
    options = parser.parse_args()
    options.output = options.output.resolve()
    if options.output == ROOT or ROOT in options.output.parents:
        parser.error("Use an output directory outside the repository")
    {"build": build, "package": package}[options.command](options)
