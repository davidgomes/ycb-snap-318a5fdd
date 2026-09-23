// Package snapshot captures and restores WebAssembly linear memory across
// multiple modules at a single point in time.
//
// A Coordinator assigns monotonically increasing versions and is safe for
// concurrent use. Snapshots expose fully reconstructed memory, a gzip
// compressed form, and byte-level comparisons.
package snapshot
