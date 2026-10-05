# Wider H.264 decoder conformance

The decoder accepts progressive 8-bit YUV420 Annex B pictures without slice groups. `decode/picture.go` rejects other formats before reconstruction. The four [external reference and weighting vectors](phase4-fixtures.md) and three checked-in state fixtures do not establish support beyond that scope.

Each track below needs a separate proposal and fixtures before implementation. Keep exact FFmpeg output comparisons in display order, with filtering enabled and disabled, plus focused primitive tests for any defect. Do not relax the format guard to make a fixture pass.

## FMO slice groups

PPS slice-group syntax is parsed; macroblock-to-slice-group reconstruction is absent. Start with a small progressive 8-bit YUV420 fixture for one chosen map type. Assert the PPS map type and group count, decode all mapped macroblocks once, and compare visible Y, U and V samples exactly. Add other map types one at a time with explicit ASO/multi-slice ownership and neighbour-availability tests. A gate for one map type does not imply the others work.

## Field pictures and MBAFF

Use separate fixtures for field-coded pictures and macroblock-adaptive frame/field pictures. Test picture order, field reference lists, motion-vector scaling, intra/inter neighbour selection, and deblocking across field/frame boundaries. Verify display-order output and both filtering modes against the pinned oracle for each type. Progressive frame results do not cover either mode.

## Other chroma formats and bit depths

Treat 4:2:2, 4:4:4, and bit depths above eight as separate formats. Each needs validated SPS/PPS geometry and scaling, frame storage and output contracts, intra/inter interpolation, transforms, weighting, chroma QP and deblocking. Keep the existing 8-bit YUV420 scalar/SIMD oracle exact. Add one bounded fixture per format and bit depth before expanding reconstruction; test visible plane dimensions and sample depth as well as sample equality.

## Gates and evidence

For each new format, pin input SHA-256, provenance and oracle version. Keep downloaded vectors outside Git. Keep FFmpeg output and traces in project-owned scratch only while comparing them; delete those captures after analysis. Record fixture availability with a strict gate; ordinary unit tests may skip external media but must name the missing input. Run full, race, `purego`, vet and Linux ARM64 cross-build checks. Native ARM64 timing requires that hardware. The historical BBB fixture remains a separate strict gate until its exact bytes are restored.
