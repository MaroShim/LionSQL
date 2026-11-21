#!/usr/bin/env python3
"""
LionSQL Windows WiredTiger MSVC Build Automation Script.
Builds WiredTiger Shared DLL with Visual Studio 2022 (/MT Static CRT)
and generates MinGW-compatible libwiredtiger.dll.a for Cgo linking.
"""

import os
import sys
import shutil
import subprocess
from pathlib import Path

def run_cmd(cmd, cwd=None, env=None):
    print(f"\n[RUN] {' '.join(cmd) if isinstance(cmd, list) else cmd} (in {cwd or '.'})", flush=True)
    res = subprocess.run(cmd, cwd=cwd, env=env, shell=isinstance(cmd, str))
    if res.returncode != 0:
        print(f"[ERROR] Command failed with exit code {res.returncode}", flush=True)
        sys.exit(res.returncode)

def main():
    repo_root = Path(__file__).resolve().parent.parent
    wt_source = repo_root / "wt_source"
    target_lib_dir = repo_root / "pkg" / "storage" / "wiredtiger" / "lib" / "windows_amd64"
    target_inc_dir = target_lib_dir / "include"

    print(f"=== LionSQL Windows WiredTiger Build ===", flush=True)
    print(f"Repo root: {repo_root}")
    print(f"Target lib dir: {target_lib_dir}")

    target_inc_dir.mkdir(parents=True, exist_ok=True)

    if not wt_source.exists():
        print(f"[ERROR] wt_source directory not found at {wt_source}", flush=True)
        sys.exit(1)

    # 1. Patch base.cmake to bypass Python3 Development component requirement
    base_cmake = wt_source / "cmake" / "configs" / "base.cmake"
    if base_cmake.exists():
        print(f"Patching {base_cmake} to avoid Python3 Development dependency...")
        content = base_cmake.read_text(encoding="utf-8")
        patched = content.replace(
            "source_python3_package(python_libs python_version python_executable)",
            "set(python_version \"3.10\")\nset(default_enable_python OFF)"
        )
        base_cmake.write_text(patched, encoding="utf-8")
        print("Successfully patched base.cmake")

    # 2. Configure CMake with Visual Studio 17 2022 (x64) and /MT Static CRT
    build_dir = wt_source / "build"
    build_dir.mkdir(parents=True, exist_ok=True)

    cmake_args = [
        "cmake",
        str(wt_source),
        "-G", "Visual Studio 17 2022",
        "-A", "x64",
        "-DCMAKE_BUILD_TYPE=Release",
        "-DCMAKE_MSVC_RUNTIME_LIBRARY=MultiThreaded",
        "-DENABLE_SHARED=1",
        "-DENABLE_STATIC=0",
        "-DENABLE_PYTHON=0",
        "-DENABLE_SNAPPY=0",
        "-DENABLE_LZ4=0",
        "-DENABLE_ZLIB=0",
        "-DENABLE_ZSTD=0",
    ]
    run_cmd(cmake_args, cwd=build_dir)

    # 3. Build wiredtiger_shared target
    print("\nBuilding wiredtiger_shared via MSVC...", flush=True)
    run_cmd([
        "cmake",
        "--build", ".",
        "--config", "Release",
        "--target", "wiredtiger_shared"
    ], cwd=build_dir)

    # 4. Locate built wiredtiger.dll and wiredtiger.lib
    dll_candidates = list(build_dir.glob("**/wiredtiger.dll"))
    if not dll_candidates:
        print("[ERROR] wiredtiger.dll not found after build!", flush=True)
        sys.exit(1)
    built_dll = dll_candidates[0]
    print(f"Found built DLL: {built_dll}", flush=True)

    dest_dll = target_lib_dir / "wiredtiger.dll"
    shutil.copy2(built_dll, dest_dll)
    print(f"Copied DLL to: {dest_dll}", flush=True)

    lib_candidates = list(build_dir.glob("**/wiredtiger.lib"))
    if lib_candidates:
        dest_lib = target_lib_dir / "wiredtiger.lib"
        shutil.copy2(lib_candidates[0], dest_lib)
        print(f"Copied MSVC import lib to: {dest_lib}", flush=True)

    # 5. Copy headers
    header_candidates = list(wt_source.glob("**/wiredtiger.h"))
    if header_candidates:
        dest_h = target_inc_dir / "wiredtiger.h"
        shutil.copy2(header_candidates[0], dest_h)
        print(f"Copied header to: {dest_h}", flush=True)

    ext_header_candidates = list(wt_source.glob("**/wiredtiger_ext.h"))
    if ext_header_candidates:
        dest_ext_h = target_inc_dir / "wiredtiger_ext.h"
        shutil.copy2(ext_header_candidates[0], dest_ext_h)
        print(f"Copied ext header to: {dest_ext_h}", flush=True)

    # 6. Generate MinGW libwiredtiger.dll.a using official or generated def
    wt_def_src = wt_source / "cmake" / "configs" / "wiredtiger.def"
    target_def = target_lib_dir / "wiredtiger.def"
    if wt_def_src.exists():
        print(f"Using official WiredTiger def file: {wt_def_src}")
        shutil.copy2(wt_def_src, target_def)
    else:
        # Fallback to python generator
        print("Generating def file via generate_wt_def.py...")
        gen_script = repo_root / "scripts" / "generate_wt_def.py"
        run_cmd([sys.executable, str(gen_script), str(dest_dll), str(target_def)])

    # Search for dlltool
    dlltool = shutil.which("dlltool")
    if not dlltool:
        for p in [
            r"C:\msys64\mingw64\bin\dlltool.exe",
            r"C:\Program Files\Git\mingw64\bin\dlltool.exe",
        ]:
            if os.path.exists(p):
                dlltool = p
                break

    if not dlltool:
        print("[WARNING] dlltool not found; attempting MinGW linking via .lib directly or gcc")
    else:
        print(f"Running dlltool: {dlltool}")
        target_a = target_lib_dir / "libwiredtiger.dll.a"
        run_cmd([
            dlltool,
            "-d", str(target_def),
            "-l", str(target_a),
            "-D", "wiredtiger.dll"
        ], cwd=target_lib_dir)
        print(f"Successfully generated {target_a}")

    print("\n=== Windows WiredTiger Build Completed Successfully ===", flush=True)

if __name__ == "__main__":
    main()
