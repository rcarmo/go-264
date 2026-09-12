// Package lc parses one bounded AAC-LC raw_data_block into fixed-size
// per-channel syntax structures and reconstructs window-major spectra.
//
// Scope is intentionally narrow:
//   - AAC-LC only
//   - 1024/128 transform family only
//   - one SCE for mono or one CPE for stereo
//   - optional DSE/FIL metadata capture
//   - dequantisation, M/S/intensity/PNS stereo and TNS reconstruction
//   - caller-owned filterbank, PRNG and container timing
//
// The parser is a Go port of syntax and table material from the MIT-licensed
// oxideav-aac project, sourced from these files at commit
// 7dcb2f4a9e6f7ccfa6b199342aeb95861dc57885:
//
//   - src/ics_info.rs
//   - src/ics_body.rs
//   - src/section_data.rs
//   - src/scale_factor_data.rs
//   - src/spectral_data.rs
//   - src/tns_data.rs
//   - src/raw_data_block.rs
//   - src/extension_payload.rs
//   - src/swb_offset.rs
package lc
