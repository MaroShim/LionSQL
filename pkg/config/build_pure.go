//go:build !(cgo && wiredtiger)

package config

// BuildTags returns the compiled storage engine build tag descriptor.
func BuildTags() string {
	return "pure-go"
}
