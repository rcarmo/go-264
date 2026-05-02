# go-264

H.264/AVC encoder and decoder in pure Go with SIMD assembly and optional GPU acceleration.

## Goals

- **Pure Go** — no CGo, static binary, cross-platform
- **SIMD assembly** — AVX2+FMA (amd64), NEON (arm64) for hot paths
- **GPU compute** — optional PTX kernels via purego (no CUDA toolkit needed)
- **Conformant** — pass ITU H.264 decoder conformance tests
- **Practical** — encode 1080p in real-time on modern hardware

## Decoder Completion Matrix

| Component | Go (scalar) | AVX2 (amd64) | NEON (arm64) | GPU (PTX) | Tests |
|---|---|---|---|---|---|
| **NAL Parser** — Annex B, start codes, emulation prevention | ✅ | — | — | — | 5 + 2 fuzz |
| **Bitstream Reader** — Exp-Golomb, fixed-length codes | ✅ | — | — | — | 4 + 2 fuzz |
| **SPS / PPS** — Baseline + High profile parsing | ✅ | — | — | — | 3 + 2 fuzz |
| **Slice Header** — I/P/B type, QP, deblocking params | ✅ | — | — | — | 1 |
| **CAVLC Entropy** — coeff_token, levels, zeros, run_before | ✅ | — | — | — | 6 + 1 fuzz |
| **CABAC Entropy** — Context-adaptive binary arithmetic | ✅ | — | — | — | 6 + 1 fuzz |
| **Intra Prediction 4×4** — 9 modes (V, H, DC, diagonal…) | ✅ | ⬜ | ⬜ | ⬜ | 3 |
| **Intra Prediction 16×16** — V, H, DC, Plane | ✅ | ⬜ | ⬜ | ⬜ | 2 |
| **Inter Prediction** — Motion compensation, subpel filter | ✅ | ⬜ | ⬜ | ⬜ | 2 |
| **4×4 Integer DCT** — Forward + inverse transform | ✅ | ⬜ | ⬜ | ⬜ | 2 + 1 fuzz |
| **8×8 Integer DCT** — High profile transform | ✅ | ⬜ | ⬜ | ⬜ | 3 + bench |
| **Quantization** — Quant + dequant, all QP levels | ✅ | — | — | — | 1 + 1 fuzz |
| **Deblocking Filter** — Normal + strong filter, luma | ✅ | ⬜ | ⬜ | ⬜ | 2 |
| **Frame / DPB** — YUV 4:2:0, reference management | ✅ | — | — | — | 4 |
| **I-Frame Decode** — End-to-end, verified with ffmpeg | ✅ | ⬜ | ⬜ | ⬜ | 2 + 1 fuzz |
| **P-Frame Decode** — Motion vectors + inter prediction | ✅ | ⬜ | ⬜ | ⬜ | 4 |
| **B-Frame Decode** — Bidirectional prediction | ⬜ | ⬜ | ⬜ | ⬜ | — |

**Legend:** ✅ Done · 🔶 Partial · ⬜ Planned · — Not applicable

**Summary:** 15/17 Go scalar · 0/17 SIMD · 0/17 GPU
**Tests:** 44 unit + 10 fuzz targets (23.6M fuzz executions, 0 crashes)
**Code:** 3,649 lines across 31 files, 8 packages

## Architecture

```
go-264/
├── nal/          NAL parser (SPS, PPS, SEI, bitstream)
├── slice/        Slice/macroblock decoding
├── pred/         Intra/inter prediction
├── transform/    DCT, quantization (+ SIMD assembly)
├── entropy/      CAVLC, CABAC
├── filter/       Deblocking filter (+ SIMD)
├── frame/        YUV frame management, DPB
├── encode/       Encoder pipeline, rate control
├── me/           Motion estimation (+ SIMD + GPU)
├── gpu/          CUDA via purego (reused from go-tinygrad)
└── cmd/
    ├── decode/   CLI decoder
    └── encode/   CLI encoder
```

## Leveraging go-tinygrad

This project reuses the GPU compute framework from [go-tinygrad](https://github.com/rcarmo/go-tinygrad):
- CUDA bindings via purego (no CGo)
- DevBuf device-agnostic buffers
- PTX kernel compilation at runtime
- Graceful CPU fallback when no GPU

## License

MIT
