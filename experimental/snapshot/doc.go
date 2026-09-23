// Package snapshot captures consistent linear-memory snapshots across multiple
// WebAssembly modules.
//
// A Coordinator assigns monotonically increasing versions and serializes
// capture and restore so the modules in one snapshot share a single cut.
// Memory captured into a Snapshot stays fixed: Data and Tags return deep
// copies, while SetTag is the only way to attach metadata.
package snapshot
