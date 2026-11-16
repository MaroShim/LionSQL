#!/usr/bin/env bash
# ==============================================================================
# scripts/build_wiredtiger_windows.sh
# Hermetic Docker builder for WiredTiger static library targeting Windows (amd64)
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
DEST_DIR="${ROOT_DIR}/pkg/storage/wiredtiger/lib/windows_amd64"
WT_VERSION="${1:-11.3.1}"

mkdir -p "${DEST_DIR}/include"

echo "=== Building WiredTiger v${WT_VERSION} for Windows x64 via Docker & MinGW-w64 ==="

docker run --platform linux/amd64 --rm \
    -v "${DEST_DIR}:/out" \
    ubuntu:22.04 bash -c "
        set -euo pipefail
        export DEBIAN_FRONTEND=noninteractive
        echo 'Installing MinGW cross-compiler and build tools...'
        apt-get update -qq && apt-get install -y -qq --no-install-recommends \
            git ca-certificates cmake ninja-build python3 mingw-w64

        echo 'Cloning WiredTiger v${WT_VERSION}...'
        git clone --depth 1 --branch '${WT_VERSION}' https://github.com/wiredtiger/wiredtiger.git /tmp/wt
        cd /tmp/wt

        echo 'Applying cross-compilation patches for MinGW-w64...'
        # 1. Skip Python Development component search in cross mode
        sed -i 's/source_python3_package.*/set(python_version \"3.10\")/' cmake/configs/base.cmake
        # 2. Exclude non-library subdirectories (benchmarks, examples, tests, tools)
        sed -i 's/add_subdirectory(bench/# add_sub/g' CMakeLists.txt
        sed -i 's/add_subdirectory(examples)/# add_sub/g' CMakeLists.txt
        sed -i 's/add_subdirectory(test)/# add_sub/g' CMakeLists.txt
        sed -i 's/add_subdirectory(tools/# add_sub/g' CMakeLists.txt

        # 3. Guard MSVC-specific compile options in base.cmake and x86/windows/config.cmake
        sed -i 's/if(WT_WIN)/if(WT_WIN AND MSVC)/g' cmake/configs/base.cmake
        sed -i 's/set(SPINLOCK_TYPE "msvc"/set(SPINLOCK_TYPE "gcc"/' cmake/configs/x86/windows/config.cmake
        sed -i 's/add_compile_options(/# add_compile_options(/g' cmake/configs/x86/windows/config.cmake
        sed -i 's/string(APPEND win_link_flags/# string(APPEND win_link_flags/g' cmake/configs/x86/windows/config.cmake

        # 4. MinGW-w64 compatibility in src/include/os_windows.h:
        # a. timespec guard: Only evaluate on MSVC, as MinGW already defines struct timespec in sys/types.h
        sed -i 's/#if _MSC_VER < 1900/#if defined(_MSC_VER) \&\& (_MSC_VER < 1900)/' src/include/os_windows.h
        # b. ssize_t guard: MinGW defines ssize_t in corecrt.h/sys/types.h
        sed -i 's/#ifndef HAVE_SSIZE_T/#if !defined(HAVE_SSIZE_T) \&\& !defined(__MINGW32__)/' src/include/os_windows.h

        # 5. crc32-x86.c duplicate definition fix:
        # MinGW-w64 defines both __x86_64__ and _M_AMD64. Guard MSVC-specific _M_AMD64 block with _MSC_VER.
        sed -i 's/#if defined(_M_AMD64)/#if defined(_MSC_VER) \&\& defined(_M_AMD64)/g' src/checksum/x86/crc32-x86.c

        mkdir build && cd build

        echo 'Configuring CMake for MinGW-w64 Windows x64...'
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
            -DENABLE_STATIC=1 \
            -DENABLE_SHARED=0 \
            -DHAVE_ATTACH=0 \
            -DENABLE_SNAPPY=0 \
            -DENABLE_LZ4=0 \
            -DENABLE_ZLIB=0 \
            -DENABLE_ZSTD=0 \
            -DENABLE_PYTHON=0

        echo 'Compiling WiredTiger static library (target: wiredtiger_static)...'
        ninja wiredtiger_static

        echo 'Extracting artifacts to host /out directory...'
        find . -name 'libwiredtiger*.a' -exec cp -v {} /out/libwiredtiger.a \;
        find /tmp/wt -name 'wiredtiger.h' -exec cp -v {} /out/include/ \;
        find /tmp/wt -name 'wiredtiger_ext.h' -exec cp -v {} /out/include/ \; 2>/dev/null || true

        echo 'Build complete! Generated artifacts:'
        ls -lh /out/libwiredtiger.a
        ls -lh /out/include/
    "

echo "=== Successfully built and vendored Windows WiredTiger static bundle! ==="
ls -lh "${DEST_DIR}/libwiredtiger.a"
ls -lh "${DEST_DIR}/include"
