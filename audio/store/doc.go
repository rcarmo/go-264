// Package store provides durable private-directory checkpoint storage for
// canonical interleaved signed 16-bit PCM.
//
// It is a narrow single-writer API for resumable decode pipelines: callers
// create a store with an immutable source/content key, append bounded canonical
// PCM segments, then later reopen the store, validate the committed manifest and
// committed segment hashes, and resume decoding from Frames(). The store does
// not guess or recreate codec/container state; callers resume by reopening the
// original decoder configuration and seeking canonical output to Frames() after
// they verify the same source SHA-256 and decoding configuration identifier.
//
// Directories are caller-chosen and must be private and trusted. This package
// does not claim malicious-concurrency, symlink-sandbox or path-containment
// protection. Segment file names and lookups are derived only from segment
// indices; no manifest-provided path is trusted.
package store
