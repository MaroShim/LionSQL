//go:build windows

package wiredtiger

// embeddedWiredTigerDLL holds the precompiled wiredtiger.dll payload when bundled during release packaging.
var embeddedWiredTigerDLL []byte
