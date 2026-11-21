#!/usr/bin/env bash
# ==============================================================================
# scripts/build_wiredtiger_windows_dll.sh
# Hermetic Docker builder for WiredTiger Shared DLL targeting Windows (amd64)
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
DEST_DIR="${ROOT_DIR}/pkg/storage/wiredtiger/lib/windows_amd64"
WT_VERSION="${1:-11.3.1}"

mkdir -p "${DEST_DIR}/include"

echo "=== Building WiredTiger v${WT_VERSION} Windows x64 DLL via Docker & MinGW-w64 ==="

docker run --platform linux/amd64 --rm \
    -v "${DEST_DIR}:/out" \
    ubuntu:22.04 bash -c "
        set -euo pipefail
        export DEBIAN_FRONTEND=noninteractive
        echo 'Installing MinGW cross-compiler and build tools...'
        apt-get update -qq && apt-get install -y -qq --no-install-recommends \
            git ca-certificates cmake ninja-build python3 mingw-w64 binutils-mingw-w64

        echo 'Cloning WiredTiger v${WT_VERSION}...'
        git clone --depth 1 --branch '${WT_VERSION}' https://github.com/wiredtiger/wiredtiger.git /tmp/wt
        cd /tmp/wt

        echo 'Applying cross-compilation patches for MinGW-w64...'
        sed -i 's/source_python3_package.*/set(python_version \"3.10\")/' cmake/configs/base.cmake
        sed -i 's/add_subdirectory(bench/# add_sub/g' CMakeLists.txt
        sed -i 's/add_subdirectory(examples)/# add_sub/g' CMakeLists.txt
        sed -i 's/add_subdirectory(test)/# add_sub/g' CMakeLists.txt
        sed -i 's/add_subdirectory(tools/# add_sub/g' CMakeLists.txt

        sed -i 's/if(WT_WIN)/if(WT_WIN AND MSVC)/g' cmake/configs/base.cmake
        sed -i 's/set(SPINLOCK_TYPE "msvc"/set(SPINLOCK_TYPE "gcc"/' cmake/configs/x86/windows/config.cmake
        sed -i 's/add_compile_options(/# add_compile_options(/g' cmake/configs/x86/windows/config.cmake
        sed -i 's/string(APPEND win_link_flags/# string(APPEND win_link_flags/g' cmake/configs/x86/windows/config.cmake

        sed -i 's/#if _MSC_VER < 1900/#if defined(_MSC_VER) \&\& (_MSC_VER < 1900)/' src/include/os_windows.h
        sed -i 's/#ifndef HAVE_SSIZE_T/#if !defined(HAVE_SSIZE_T) \&\& !defined(__MINGW32__)/' src/include/os_windows.h
        sed -i 's/#if defined(_M_AMD64)/#if defined(_MSC_VER) \&\& defined(_M_AMD64)/g' src/checksum/x86/crc32-x86.c

        mkdir build && cd build

        echo 'Configuring CMake for MinGW-w64 Shared DLL...'
        cmake .. -G Ninja \
            -DCMAKE_SYSTEM_NAME=Windows \
            -DCMAKE_SYSTEM_PROCESSOR=x86_64 \
            -DCMAKE_C_COMPILER=x86_64-w64-mingw32-gcc \
            -DCMAKE_CXX_COMPILER=x86_64-w64-mingw32-g++ \
            -DWT_ARCH=x86 \
            -DWT_OS=windows \
            -DSPINLOCK_TYPE=gcc \
            -DCMAKE_C_FLAGS='-w -D_FILE_OFFSET_BITS=64 -DHAVE_SSIZE_T=1' \
            -DCMAKE_CXX_FLAGS='-w -D_FILE_OFFSET_BITS=64 -DHAVE_SSIZE_T=1' \
            -DCMAKE_BUILD_TYPE=Release \
            -DENABLE_STATIC=0 \
            -DENABLE_SHARED=1 \
            -DHAVE_ATTACH=0 \
            -DENABLE_SNAPPY=0 \
            -DENABLE_LZ4=0 \
            -DENABLE_ZLIB=0 \
            -DENABLE_ZSTD=0 \
            -DENABLE_PYTHON=0

        echo 'Compiling WiredTiger Shared DLL...'
        ninja wiredtiger

        echo 'Extracting DLL and generating import library...'
        find . -name 'libwiredtiger*.dll' -exec cp -v {} /out/wiredtiger.dll \; || find . -name 'wiredtiger.dll' -exec cp -v {} /out/wiredtiger.dll \;
        find /tmp/wt -name 'wiredtiger.h' -exec cp -v {} /out/include/ \;
        find /tmp/wt -name 'wiredtiger_ext.h' -exec cp -v {} /out/include/ \; 2>/dev/null || true

        if [ -f /out/wiredtiger.dll ]; then
            cd /out
            gendef wiredtiger.dll || true
            if [ -f wiredtiger.def ]; then
                x86_64-w64-mingw32-dlltool -d wiredtiger.def -l libwiredtiger.dll.a -D wiredtiger.dll
            fi
        fi

        echo 'Build complete! Generated artifacts:'
        ls -lh /out/
    "

chmod +x "${DEST_DIR}"/*.dll 2>/dev/null || true
echo "=== Successfully built Windows WiredTiger DLL bundle! ==="
