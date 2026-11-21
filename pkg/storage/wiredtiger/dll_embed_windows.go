//go:build windows && cgo && wiredtiger

package wiredtiger

import _ "embed"

// embeddedWiredTigerDLL holds the precompiled wiredtiger.dll payload when bundled during release packaging.
//
//go:embed lib/windows_amd64/wiredtiger.dll
var embeddedWiredTigerDLL []byte
