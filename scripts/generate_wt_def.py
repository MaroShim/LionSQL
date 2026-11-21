#!/usr/bin/env python3
"""
Generate Windows module definition (.def) file from wiredtiger.dll exports using dumpbin.
Used to create MinGW-compatible import library (libwiredtiger.dll.a).
"""

import sys
import subprocess
from pathlib import Path

def main():
    if len(sys.argv) < 3:
        print("Usage: generate_wt_def.py <path_to_dll> <output_def_path>")
        sys.exit(1)

    dll_path = Path(sys.argv[1]).resolve()
    def_path = Path(sys.argv[2]).resolve()

    if not dll_path.exists():
        print(f"Error: DLL not found at {dll_path}")
        sys.exit(1)

    print(f"Extracting exports from: {dll_path}")
    try:
        out = subprocess.check_output(
            ["dumpbin", "/EXPORTS", str(dll_path)],
            stderr=subprocess.STDOUT
        ).decode("utf-8", errors="ignore")
    except Exception as e:
        print(f"Error running dumpbin: {e}")
        sys.exit(1)

    exports = []
    in_exports = False
    for line in out.splitlines():
        line = line.strip()
        if "ordinal" in line and "hint" in line and "name" in line:
            in_exports = True
            continue
        if in_exports:
            if not line or "Summary" in line:
                break
            parts = line.split()
            # Expecting format: <ordinal> <hint> <RVA> <name>
            if len(parts) >= 4 and parts[0].isdigit():
                sym = parts[3]
                exports.append(sym)
            elif len(parts) == 3 and parts[0].isdigit():
                # Some symbols might omit hint
                sym = parts[2]
                exports.append(sym)

    if not exports:
        print("Warning: No exports found via dumpbin! Falling back to standard wiredtiger API symbols.")
        exports = [
            "wiredtiger_open",
            "wiredtiger_strerror",
            "wiredtiger_version",
            "wiredtiger_extension_terminate",
        ]

    print(f"Found {len(exports)} exported symbols.")
    with open(def_path, "w", encoding="utf-8") as f:
        f.write("EXPORTS\n")
        for sym in sorted(exports):
            f.write(f"    {sym}\n")

    print(f"Successfully generated {def_path}")

if __name__ == "__main__":
    main()
