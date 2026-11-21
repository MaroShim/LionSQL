//go:build windows && (!cgo || !wiredtiger)

package wiredtiger

// embeddedWiredTigerDLL is empty when building without wiredtiger cgo support.
var embeddedWiredTigerDLL []byte
