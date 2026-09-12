package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rcarmo/go-264/audio/pcm"
)

const (
	manifestName          = "manifest.json"
	segmentNamePrefix     = "seg-"
	segmentNameSuffix     = ".s16"
	segmentIndexHexWidth  = 16
	tempManifestPrefix    = manifestName + ".tmp-"
	maxSegmentPayloadByte = 1 << 20
	hardMaxManifestBytes  = 1 << 20
	defaultMaxSegments    = 4096
	hardMaxSegments       = 65535
	maxConfigIDBytes      = 256
	writeChunkBytes       = 32768
)

var (
	ErrKeyMismatch         = errors.New("audio/store: key mismatch")
	ErrConflict            = errors.New("audio/store: existing segment conflict")
	ErrUncertainDurability = errors.New("audio/store: uncertain durability; reopen required")
	ErrUnusable            = errors.New("audio/store: store unusable until reopen")
)

// Key binds a store to one exact decoded source and decode configuration.
// SourceSHA256 must be exactly 64 lowercase or uppercase hexadecimal digits.
// DecodeConfig is caller-defined and compared byte-for-byte on Open.
type Key struct {
	SourceSHA256 string `json:"source_sha256"`
	DecodeConfig string `json:"decode_config"`
}

// Options bound manifest growth. Zero selects defaults. Hard caps still apply:
// manifest bytes may never exceed 1 MiB and segment payloads may never exceed
// 1 MiB of canonical PCM bytes.
type Options struct {
	MaxSegments      int `json:"max_segments"`
	MaxManifestBytes int `json:"max_manifest_bytes"`
}

// Orphan reports an unreferenced deterministic segment file found during Open.
// It is not part of the committed manifest. A later Append for the same index
// will reuse it only if its bytes exactly match the caller's new payload.
type Orphan struct {
	Name  string
	Index int
	Bytes int64
}

// Store is sequential. Append, ReadAt, Open/Create and Close must not race.
type Store struct {
	dir      string
	manifest diskManifest
	orphans  []Orphan
	closed   bool
	unusable bool
}

type diskManifest struct {
	Version  int           `json:"version"`
	Key      Key           `json:"key"`
	Metadata pcm.Metadata  `json:"metadata"`
	Options  Options       `json:"options"`
	Frames   int64         `json:"frames"`
	Segments []diskSegment `json:"segments"`
}

type diskSegment struct {
	Index      int    `json:"index"`
	StartFrame int64  `json:"start_frame"`
	Frames     int64  `json:"frames"`
	Bytes      int64  `json:"bytes"`
	SHA256     string `json:"sha256"`
}

var (
	createTempFile = os.CreateTemp
	renameFile     = os.Rename
	syncFileFunc   = func(f *os.File) error { return f.Sync() }
	syncDirFunc    = syncDir
	afterWriteHook func()
)

// Create initialises a new empty store in dir. dir must either not exist, in
// which case it is created with mode 0700, or already exist and be empty.
// metadata describes the canonical S16 stream and is preserved as provided;
// Frames() tracks committed canonical output independently.
func Create(ctx context.Context, dir string, key Key, metadata pcm.Metadata, options Options) (*Store, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", pcm.ErrMalformed)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dir == "" {
		return nil, fmt.Errorf("%w: empty store directory", pcm.ErrMalformed)
	}
	var err error
	if key, err = validateKey(key); err != nil {
		return nil, err
	}
	if metadata, err = validateMetadata(metadata); err != nil {
		return nil, err
	}
	if options, err = options.validated(); err != nil {
		return nil, err
	}
	created, err := ensureCreateDir(dir)
	if err != nil {
		return nil, err
	}
	if created {
		if err := syncDirFunc(filepath.Dir(dir)); err != nil {
			_ = os.Remove(dir)
			return nil, err
		}
	}
	m := diskManifest{Version: 1, Key: key, Metadata: metadata, Options: options, Frames: 0, Segments: nil}
	if _, err := marshalManifest(m); err != nil {
		return nil, err
	}
	if err := writeManifestAtomic(ctx, dir, m); err != nil {
		return nil, err
	}
	return &Store{dir: dir, manifest: m}, nil
}

// Open validates the manifest, key, canonical format, committed segment hashes
// and contiguous frame coverage. Extra deterministic segment files not named in
// the manifest are reported as orphans and do not change Frames().
func Open(ctx context.Context, dir string, key Key) (*Store, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: nil context", pcm.ErrMalformed)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dir == "" {
		return nil, fmt.Errorf("%w: empty store directory", pcm.ErrMalformed)
	}
	var err error
	if key, err = validateKey(key); err != nil {
		return nil, err
	}
	m, manifestBytes, err := readManifestFile(ctx, dir)
	if err != nil {
		return nil, err
	}
	m, err = validateManifest(m, manifestBytes, key)
	if err != nil {
		return nil, err
	}
	if err := validateSegmentFiles(ctx, dir, m); err != nil {
		return nil, err
	}
	orphans, err := scanOrphans(dir, len(m.Segments))
	if err != nil {
		return nil, err
	}
	return &Store{dir: dir, manifest: m, orphans: orphans}, nil
}

// Frames returns the last atomically committed canonical frame count.
func (s *Store) Frames() int64 {
	if s == nil {
		return 0
	}
	return s.manifest.Frames
}

// Metadata returns the caller-provided canonical stream metadata stored in the
// manifest. It does not synthesize committed frame counts; use Frames() for the
// committed resume point.
func (s *Store) Metadata() pcm.Metadata {
	if s == nil {
		return pcm.Metadata{}
	}
	return s.manifest.Metadata
}

// Orphans returns a copy of currently known unreferenced deterministic segment
// files. Create starts empty. Open populates this from directory scanning.
func (s *Store) Orphans() []Orphan {
	if s == nil || len(s.orphans) == 0 {
		return nil
	}
	out := make([]Orphan, len(s.orphans))
	copy(out, s.orphans)
	return out
}

// Append atomically adds one immutable canonical PCM segment. samples are
// interleaved signed 16-bit samples owned by the caller; the store does not
// retain the slice after return. len(samples) must be a whole-frame multiple of
// Metadata().Output.Channels and the raw payload must be at most 1 MiB.
func (s *Store) Append(ctx context.Context, samples []int16) error {
	if s == nil || s.closed {
		return pcm.ErrClosed
	}
	if s.unusable {
		return ErrUnusable
	}
	if ctx == nil {
		return fmt.Errorf("%w: nil context", pcm.ErrMalformed)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(samples) == 0 {
		return nil
	}
	ch := s.manifest.Metadata.Output.Channels
	if ch <= 0 || len(samples)%ch != 0 {
		return fmt.Errorf("%w: append buffer shape", pcm.ErrMalformed)
	}
	if len(samples) > maxSegmentPayloadByte/2 {
		return fmt.Errorf("%w: segment payload cap", pcm.ErrLimit)
	}
	if len(s.manifest.Segments) >= s.manifest.Options.MaxSegments {
		return fmt.Errorf("%w: segment count cap", pcm.ErrLimit)
	}
	frames := int64(len(samples) / ch)
	if frames > s.manifest.Metadata.Output.Frames-s.manifest.Frames {
		return fmt.Errorf("%w: declared output frame cap", pcm.ErrLimit)
	}
	if s.manifest.Frames > math.MaxInt64-frames {
		return fmt.Errorf("%w: frame count overflow", pcm.ErrLimit)
	}
	payload := encodeSamples(samples)
	digest := sha256Hex(payload)
	index := len(s.manifest.Segments)
	finalPath := filepath.Join(s.dir, segmentFileName(index))
	if err := ensureInstalledSegment(ctx, s, finalPath, index, payload, digest); err != nil {
		return err
	}
	seg := diskSegment{Index: index, StartFrame: s.manifest.Frames, Frames: frames, Bytes: int64(len(payload)), SHA256: digest}
	next := s.manifest
	next.Frames += frames
	next.Segments = append(append([]diskSegment(nil), s.manifest.Segments...), seg)
	if _, err := marshalManifest(next); err != nil {
		return err
	}
	if err := writeManifestAtomic(ctx, s.dir, next); err != nil {
		if errors.Is(err, ErrUncertainDurability) {
			s.unusable = true
		}
		return err
	}
	s.manifest = next
	s.removeOrphan(index)
	return nil
}

// ReadAt reads canonical PCM starting at frame into dst and returns a scalar
// sample count, not a frame count. frame is expressed in canonical frames. A
// short final read returns n>0 with io.EOF. The store remains sequential.
func (s *Store) ReadAt(ctx context.Context, dst []int16, frame int64) (int, error) {
	if s == nil || s.closed {
		return 0, pcm.ErrClosed
	}
	if s.unusable {
		return 0, ErrUnusable
	}
	if ctx == nil {
		return 0, fmt.Errorf("%w: nil context", pcm.ErrMalformed)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	ch := s.manifest.Metadata.Output.Channels
	if ch <= 0 || len(dst)%ch != 0 {
		return 0, fmt.Errorf("%w: read buffer shape", pcm.ErrMalformed)
	}
	if frame < 0 || frame > s.manifest.Frames {
		return 0, fmt.Errorf("%w: read frame offset", pcm.ErrMalformed)
	}
	if len(dst) == 0 {
		return 0, nil
	}
	remainingFrames := s.manifest.Frames - frame
	if remainingFrames == 0 {
		return 0, io.EOF
	}
	wantFrames := int64(len(dst) / ch)
	if wantFrames > remainingFrames {
		wantFrames = remainingFrames
	}
	wantSamples := int(wantFrames) * ch
	out := 0
	pos := frame
	for out < wantSamples {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		seg, ok := s.segmentForFrame(pos)
		if !ok {
			return out, fmt.Errorf("%w: missing segment coverage", pcm.ErrMalformed)
		}
		within := pos - seg.StartFrame
		if within < 0 || within >= seg.StartFrame+seg.Frames-seg.StartFrame {
			return out, fmt.Errorf("%w: invalid segment coverage", pcm.ErrMalformed)
		}
		takeFrames := seg.Frames - within
		needFrames := int64((wantSamples - out) / ch)
		if takeFrames > needFrames {
			takeFrames = needFrames
		}
		n, err := readSegmentRange(ctx, filepath.Join(s.dir, segmentFileName(seg.Index)), ch, within, dst[out:out+int(takeFrames)*ch])
		out += n
		pos += int64(n / ch)
		if err != nil {
			return out, err
		}
	}
	if wantFrames < int64(len(dst)/ch) {
		return out, io.EOF
	}
	return out, nil
}

// Close is idempotent.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.closed = true
	return nil
}

func (o Options) validated() (Options, error) {
	if o.MaxSegments < 0 || o.MaxManifestBytes < 0 {
		return o, fmt.Errorf("%w: negative store limits", pcm.ErrLimit)
	}
	if o.MaxSegments == 0 {
		o.MaxSegments = defaultMaxSegments
	}
	if o.MaxManifestBytes == 0 {
		o.MaxManifestBytes = hardMaxManifestBytes
	}
	if o.MaxSegments > hardMaxSegments {
		return o, fmt.Errorf("%w: segment count hard cap", pcm.ErrLimit)
	}
	if o.MaxManifestBytes > hardMaxManifestBytes {
		return o, fmt.Errorf("%w: manifest byte hard cap", pcm.ErrLimit)
	}
	return o, nil
}

func validateKey(key Key) (Key, error) {
	if len(key.SourceSHA256) != 64 {
		return key, fmt.Errorf("%w: source SHA-256 must be 64 hex characters", pcm.ErrMalformed)
	}
	for i := 0; i < len(key.SourceSHA256); i++ {
		c := key.SourceSHA256[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return key, fmt.Errorf("%w: invalid source SHA-256", pcm.ErrMalformed)
		}
	}
	if key.DecodeConfig == "" || len(key.DecodeConfig) > maxConfigIDBytes {
		return key, fmt.Errorf("%w: invalid decode configuration identifier", pcm.ErrMalformed)
	}
	for i := 0; i < len(key.DecodeConfig); i++ {
		if key.DecodeConfig[i] < 0x20 || key.DecodeConfig[i] == 0x7f {
			return key, fmt.Errorf("%w: invalid decode configuration identifier", pcm.ErrMalformed)
		}
	}
	key.SourceSHA256 = strings.ToLower(key.SourceSHA256)
	return key, nil
}

func validateMetadata(m pcm.Metadata) (pcm.Metadata, error) {
	if err := validateInfo(m.Source, false); err != nil {
		return m, err
	}
	if err := validateInfo(m.Output, true); err != nil {
		return m, err
	}
	if m.PrimingFrames < 0 || m.PaddingFrames < 0 || m.ResamplerDelayFrames < 0 || m.LeadingSilenceFrames < 0 {
		return m, fmt.Errorf("%w: negative metadata frame count", pcm.ErrMalformed)
	}
	return m, nil
}

func validateInfo(info pcm.Info, output bool) error {
	if info.SampleRate < 8000 || info.SampleRate > 192000 || (info.Channels != 1 && info.Channels != 2) || info.Frames < 0 || info.BitsPerSample < 0 {
		return fmt.Errorf("%w: invalid PCM info", pcm.ErrMalformed)
	}
	if output && (info.SampleRate > 48000 || info.Frames > int64(info.SampleRate)*14400) {
		return fmt.Errorf("%w: canonical rate/duration", pcm.ErrLimit)
	}
	if output && info.BitsPerSample != 16 {
		return fmt.Errorf("%w: canonical output must be 16-bit PCM", pcm.ErrMalformed)
	}
	return nil
}

func ensureCreateDir(dir string) (bool, error) {
	st, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.Mkdir(dir, 0700); err != nil {
				return false, err
			}
			return true, nil
		}
		return false, err
	}
	if !st.IsDir() {
		return false, fmt.Errorf("%w: store path is not a directory", pcm.ErrMalformed)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, err
	}
	if len(entries) != 0 {
		return false, fmt.Errorf("%w: create requires a new or empty directory", pcm.ErrMalformed)
	}
	return false, nil
}

func marshalManifest(m diskManifest) ([]byte, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	if len(data) > m.Options.MaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest byte cap", pcm.ErrLimit)
	}
	if len(data) > hardMaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest hard byte cap", pcm.ErrLimit)
	}
	return data, nil
}

func writeManifestAtomic(ctx context.Context, dir string, m diskManifest) error {
	data, err := marshalManifest(m)
	if err != nil {
		return err
	}
	tempPath, err := writeTempFile(ctx, dir, tempManifestPrefix, data)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	finalPath := filepath.Join(dir, manifestName)
	if err := renameFile(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := syncDirFunc(dir); err != nil {
		return fmt.Errorf("%w: manifest directory sync: %v", ErrUncertainDurability, err)
	}
	return nil
}

func readManifestFile(ctx context.Context, dir string) (diskManifest, int, error) {
	if err := ctx.Err(); err != nil {
		return diskManifest{}, 0, err
	}
	path := filepath.Join(dir, manifestName)
	st, err := os.Stat(path)
	if err != nil {
		return diskManifest{}, 0, err
	}
	if st.Size() <= 0 {
		return diskManifest{}, 0, fmt.Errorf("%w: empty manifest", pcm.ErrMalformed)
	}
	if st.Size() > hardMaxManifestBytes {
		return diskManifest{}, 0, fmt.Errorf("%w: manifest hard byte cap", pcm.ErrLimit)
	}
	if !st.Mode().IsRegular() {
		return diskManifest{}, 0, fmt.Errorf("%w: manifest file type", pcm.ErrMalformed)
	}
	f, err := os.Open(path)
	if err != nil {
		return diskManifest{}, 0, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, hardMaxManifestBytes+1))
	if err != nil {
		return diskManifest{}, 0, err
	}
	if len(data) > hardMaxManifestBytes {
		return diskManifest{}, 0, fmt.Errorf("%w: manifest byte cap", pcm.ErrLimit)
	}
	if err := ctx.Err(); err != nil {
		return diskManifest{}, 0, err
	}
	var m diskManifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return diskManifest{}, 0, fmt.Errorf("%w: manifest JSON", pcm.ErrMalformed)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return diskManifest{}, 0, fmt.Errorf("%w: manifest trailing data", pcm.ErrMalformed)
	}
	return m, len(data), nil
}

func validateManifest(m diskManifest, manifestBytes int, key Key) (diskManifest, error) {
	if m.Version != 1 {
		return m, fmt.Errorf("%w: unsupported manifest version", pcm.ErrMalformed)
	}
	var err error
	if m.Key, err = validateKey(m.Key); err != nil {
		return m, err
	}
	if m.Metadata, err = validateMetadata(m.Metadata); err != nil {
		return m, err
	}
	if m.Options, err = m.Options.validated(); err != nil {
		return m, err
	}
	if manifestBytes > m.Options.MaxManifestBytes {
		return m, fmt.Errorf("%w: manifest exceeds stored byte cap", pcm.ErrLimit)
	}
	if m.Key != key {
		return m, ErrKeyMismatch
	}
	if m.Frames < 0 {
		return m, fmt.Errorf("%w: negative committed frame count", pcm.ErrMalformed)
	}
	if len(m.Segments) > m.Options.MaxSegments {
		return m, fmt.Errorf("%w: manifest segment count cap", pcm.ErrLimit)
	}
	running := int64(0)
	for i, seg := range m.Segments {
		if seg.Index != i {
			return m, fmt.Errorf("%w: non-sequential segment index", pcm.ErrMalformed)
		}
		if seg.StartFrame != running {
			return m, fmt.Errorf("%w: non-contiguous segment coverage", pcm.ErrMalformed)
		}
		if seg.Frames <= 0 {
			return m, fmt.Errorf("%w: invalid segment frame count", pcm.ErrMalformed)
		}
		if len(seg.SHA256) != 64 {
			return m, fmt.Errorf("%w: invalid segment hash", pcm.ErrMalformed)
		}
		if _, err := hex.DecodeString(seg.SHA256); err != nil {
			return m, fmt.Errorf("%w: invalid segment hash", pcm.ErrMalformed)
		}
		bytesWanted, err := segmentBytesForFrames(seg.Frames, m.Metadata.Output.Channels)
		if err != nil {
			return m, err
		}
		if seg.Bytes != bytesWanted {
			return m, fmt.Errorf("%w: invalid segment byte count", pcm.ErrMalformed)
		}
		if seg.Bytes > maxSegmentPayloadByte {
			return m, fmt.Errorf("%w: segment payload cap", pcm.ErrLimit)
		}
		if running > math.MaxInt64-seg.Frames {
			return m, fmt.Errorf("%w: frame count overflow", pcm.ErrLimit)
		}
		running += seg.Frames
	}
	if running != m.Frames {
		return m, fmt.Errorf("%w: manifest frame coverage mismatch", pcm.ErrMalformed)
	}
	if m.Frames > m.Metadata.Output.Frames {
		return m, fmt.Errorf("%w: committed frames exceed declared output", pcm.ErrMalformed)
	}
	return m, nil
}

func validateSegmentFiles(ctx context.Context, dir string, m diskManifest) error {
	for _, seg := range m.Segments {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(dir, segmentFileName(seg.Index))
		st, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return fmt.Errorf("%w: segment file type", pcm.ErrMalformed)
		}
		if st.Size() != seg.Bytes {
			return fmt.Errorf("%w: segment size mismatch", pcm.ErrMalformed)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if int64(len(data)) != seg.Bytes {
			return fmt.Errorf("%w: segment short read", pcm.ErrMalformed)
		}
		if sha256Hex(data) != seg.SHA256 {
			return fmt.Errorf("%w: segment hash mismatch", pcm.ErrMalformed)
		}
	}
	return nil
}

func scanOrphans(dir string, committed int) ([]Orphan, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Orphan
	for _, entry := range entries {
		index, ok := parseSegmentFileName(entry.Name())
		if !ok || index < committed {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		out = append(out, Orphan{Name: entry.Name(), Index: index, Bytes: info.Size()})
	}
	return out, nil
}

func ensureInstalledSegment(ctx context.Context, s *Store, finalPath string, index int, payload []byte, digest string) error {
	st, err := os.Stat(finalPath)
	if err == nil {
		same, err := existingSegmentMatches(st, finalPath, payload, digest)
		if err != nil {
			return err
		}
		if !same {
			return fmt.Errorf("%w: segment %d already exists", ErrConflict, index)
		}
		// Reused crash orphan must be synced again before committing a reference.
		if e := syncDirFunc(s.dir); e != nil {
			s.unusable = true
			return fmt.Errorf("%w: orphan directory sync", ErrUncertainDurability)
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	tempPath, err := writeTempFile(ctx, s.dir, tempSegmentPrefix(index), payload)
	if err != nil {
		return err
	}
	if _, err := os.Stat(finalPath); err == nil {
		_ = os.Remove(tempPath)
		same, err := existingSegmentMatches(st, finalPath, payload, digest)
		if err != nil {
			return err
		}
		if !same {
			return fmt.Errorf("%w: segment %d already exists", ErrConflict, index)
		}
		return nil
	} else if !os.IsNotExist(err) {
		_ = os.Remove(tempPath)
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	// Hard link provides atomic no-clobber installation in this private directory.
	if err := os.Link(tempPath, finalPath); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Remove(tempPath); err != nil {
		s.unusable = true
		return fmt.Errorf("%w: segment temporary unlink", ErrUncertainDurability)
	}
	if err := syncDirFunc(s.dir); err != nil {
		s.unusable = true
		return fmt.Errorf("%w: segment directory sync: %v", ErrUncertainDurability, err)
	}
	return nil
}

func existingSegmentMatches(st os.FileInfo, path string, payload []byte, digest string) (bool, error) {
	if st == nil {
		var err error
		st, err = os.Stat(path)
		if err != nil {
			return false, err
		}
	}
	if !st.Mode().IsRegular() {
		return false, fmt.Errorf("%w: segment file type", pcm.ErrMalformed)
	}
	if st.Size() != int64(len(payload)) {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if int64(len(data)) != st.Size() {
		return false, fmt.Errorf("%w: segment short read", pcm.ErrMalformed)
	}
	return sha256Hex(data) == digest, nil
}

func writeTempFile(ctx context.Context, dir, pattern string, data []byte) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	f, err := createTempFile(dir, pattern)
	if err != nil {
		return "", err
	}
	keep := false
	name := f.Name()
	defer func() {
		if !keep {
			_ = f.Close()
			_ = os.Remove(name)
		}
	}()
	for off := 0; off < len(data); {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		end := off + writeChunkBytes
		if end > len(data) {
			end = len(data)
		}
		n, err := f.Write(data[off:end])
		if err != nil {
			return "", err
		}
		if n != end-off {
			return "", io.ErrShortWrite
		}
		off += n
		if afterWriteHook != nil {
			afterWriteHook()
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := syncFileFunc(f); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	keep = true
	return name, nil
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func segmentBytesForFrames(frames int64, channels int) (int64, error) {
	if frames < 0 || channels <= 0 {
		return 0, fmt.Errorf("%w: segment dimensions", pcm.ErrMalformed)
	}
	if frames > math.MaxInt64/int64(channels) {
		return 0, fmt.Errorf("%w: segment frame overflow", pcm.ErrLimit)
	}
	samples := frames * int64(channels)
	if samples > math.MaxInt64/2 {
		return 0, fmt.Errorf("%w: segment byte overflow", pcm.ErrLimit)
	}
	return samples * 2, nil
}

func encodeSamples(samples []int16) []byte {
	data := make([]byte, len(samples)*2)
	for i, v := range samples {
		binary.LittleEndian.PutUint16(data[i*2:], uint16(v))
	}
	return data
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func tempSegmentPrefix(index int) string {
	return strings.TrimSuffix(segmentFileName(index), segmentNameSuffix) + ".tmp-"
}

func segmentFileName(index int) string {
	return fmt.Sprintf("%s%0*x%s", segmentNamePrefix, segmentIndexHexWidth, uint64(index), segmentNameSuffix)
}

func parseSegmentFileName(name string) (int, bool) {
	if !strings.HasPrefix(name, segmentNamePrefix) || !strings.HasSuffix(name, segmentNameSuffix) {
		return 0, false
	}
	hexPart := strings.TrimSuffix(strings.TrimPrefix(name, segmentNamePrefix), segmentNameSuffix)
	if len(hexPart) != segmentIndexHexWidth {
		return 0, false
	}
	u, err := strconv.ParseUint(hexPart, 16, 64)
	if err != nil || u > uint64(^uint(0)>>1) {
		return 0, false
	}
	return int(u), true
}

func (s *Store) segmentForFrame(frame int64) (diskSegment, bool) {
	for _, seg := range s.manifest.Segments {
		if frame >= seg.StartFrame && frame < seg.StartFrame+seg.Frames {
			return seg, true
		}
	}
	return diskSegment{}, false
}

func readSegmentRange(ctx context.Context, path string, channels int, frameOffset int64, dst []int16) (int, error) {
	if len(dst)%channels != 0 {
		return 0, fmt.Errorf("%w: read buffer shape", pcm.ErrMalformed)
	}
	if len(dst) == 0 {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	byteOffset, err := segmentBytesForFrames(frameOffset, channels)
	if err != nil {
		return 0, err
	}
	buf := make([]byte, len(dst)*2)
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	if _, err := io.ReadFull(io.NewSectionReader(f, byteOffset, int64(len(buf))), buf); err != nil {
		return 0, fmt.Errorf("%w: read segment range", pcm.ErrMalformed)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	for i := range dst {
		dst[i] = int16(binary.LittleEndian.Uint16(buf[i*2:]))
	}
	return len(dst), nil
}

func (s *Store) removeOrphan(index int) {
	for i, orphan := range s.orphans {
		if orphan.Index == index {
			copy(s.orphans[i:], s.orphans[i+1:])
			s.orphans = s.orphans[:len(s.orphans)-1]
			return
		}
	}
}
