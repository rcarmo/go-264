# Third-party notices

The entire go-264 project is distributed under the [MIT License](LICENSE), copyright (c) 2026 Rui Carmo. Imported MIT material also retains the notices below. The root licence covers the video and audio packages, commands, scripts, tests and documentation.

## OxideAV AAC

The AAC implementation includes Go ports and generated tables from [OxideAV/oxideav-aac](https://github.com/OxideAV/oxideav-aac), revision `7dcb2f4a9e6f7ccfa6b199342aeb95861dc57885`, under the MIT License, copyright (c) 2026 Karpelès Lab Inc.

Retained package notices:

- [aacbits/MIT-NOTICE.txt](audio/aac/internal/aacbits/MIT-NOTICE.txt)
- [filterbank/MIT-NOTICE.txt](audio/aac/internal/filterbank/MIT-NOTICE.txt)
- [huffman/MIT-NOTICE.txt](audio/aac/internal/huffman/MIT-NOTICE.txt)
- [lc/MIT-NOTICE.txt](audio/aac/internal/lc/MIT-NOTICE.txt)

[Generated Huffman tables](audio/aac/internal/huffman/tables_mit.go) retain the full upstream MIT licence and source URLs. [The generator](scripts/gen_audio_huffman.go) verifies the upstream licence and table-source SHA-256 hashes before generation. No Rust library is linked or required at runtime.

The recorded development provenance includes review of non-MIT reference implementations; the AAC work is not described as clean-room. No FAAD2, LGPL go-aac or Apache aac-go implementation was imported into the audio code. Translating source code does not replace its original licence.

## External fixtures and validation tools

Synthetic fixtures authored in the project tests are covered by the project MIT licence. Referenced recordings and external test tools keep their original licences:

- MINDS-14 recordings and transcripts: PolyAI, CC-BY-4.0.
- Pyannote tutorial recording/annotation: the pinned repository's MIT notice, copyright 2020 CNRS, as documented in the fixture manifest.
- FFmpeg, model weights, Python packages and other separately installed validation tools are not distributed under the go-264 licence and are not audio runtime dependencies.

Exact source revisions, fixture hashes, attributions and supported derivative formats are recorded in [audio/testdata/README.md](audio/testdata/README.md) and [public-fixtures.json](audio/testdata/public-fixtures.json). External recordings are not bundled with go-264.
