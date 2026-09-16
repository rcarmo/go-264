package decode

import "testing"

func TestTraceConfigSnapshotsFlagsAndIntegers(t *testing.T) {
	t.Setenv("GO264_B_CABAC_TRACE", "1")
	t.Setenv("GO264_DISABLE_DEBLOCK", "yes")
	t.Setenv("GO264_P_TYPE_TRACE_LIMIT", "7")
	t.Setenv("GO264_B_TYPE_TRACE_LIMIT", "0")
	t.Setenv("GO264_B_TYPE_TRACE_POC", "-3")
	t.Setenv("GO264_P_MVP_TRACE_FROM_MB", "bad")
	t.Setenv("GO264_MOTION_SAVE_MB_LIMIT", "0")

	got := snapshotTraceConfig()
	if !got.enabled(traceBCABAC) || !got.enabled(disableDeblock) {
		t.Fatal("enabled flags were not captured")
	}
	if got.enabled(traceBMB) {
		t.Fatal("unset flag was captured")
	}
	if got.pTypeLimit != 7 || got.bTypeLimit != 20 {
		t.Fatalf("positive limits = %d, %d; want 7, 20", got.pTypeLimit, got.bTypeLimit)
	}
	if got.bTypePOC != -3 || got.pMVPFromMB != 0 || got.motionSaveLimit != 0 {
		t.Fatalf("integer snapshot = bPOC %d, fromMB %d, saveLimit %d", got.bTypePOC, got.pMVPFromMB, got.motionSaveLimit)
	}
}

func TestTraceConfigIsImmutableAfterSnapshot(t *testing.T) {
	t.Setenv("GO264_B_MVP_TRACE", "1")
	t.Setenv("GO264_TEMPORAL_DIRECT_TRACE_POC", "14")
	got := snapshotTraceConfig()

	t.Setenv("GO264_B_MVP_TRACE", "")
	t.Setenv("GO264_TEMPORAL_DIRECT_TRACE_POC", "22")
	if !got.enabled(traceBMVP) || got.temporalPOC != 14 {
		t.Fatalf("snapshot changed with environment: enabled=%t poc=%d", got.enabled(traceBMVP), got.temporalPOC)
	}
}

func TestDecodersKeepIndependentTraceSnapshots(t *testing.T) {
	t.Setenv("GO264_B_REF_TRACE", "1")
	first := NewDecoder()
	_, _ = first.Decode(nil)

	t.Setenv("GO264_B_REF_TRACE", "")
	second := NewDecoder()
	_, _ = second.Decode(nil)

	if !first.trace.enabled(traceBRef) {
		t.Fatal("first decoder snapshot changed")
	}
	if second.trace.enabled(traceBRef) {
		t.Fatal("second decoder inherited first decoder snapshot")
	}
}

func TestFirstTraceConfigFallbackAndReuse(t *testing.T) {
	t.Setenv("GO264_DIRECT_TRACE", "1")
	fallback := firstTraceConfig(nil)
	if !fallback.enabled(traceDirect) {
		t.Fatal("direct-call fallback did not snapshot the environment")
	}

	provided := snapshotTraceConfig()
	if got := firstTraceConfig([]*traceConfig{&provided}); got != &provided {
		t.Fatal("supplied snapshot was copied instead of reused")
	}
	if allocs := testing.AllocsPerRun(1000, func() {
		if firstTraceConfig([]*traceConfig{&provided}) != &provided {
			panic("snapshot")
		}
	}); allocs != 0 {
		t.Fatalf("supplied snapshot reuse allocated: %v", allocs)
	}
}

func TestMotionPOCSnapshotDoesNotMutateBase(t *testing.T) {
	base := traceConfig{motionPOC: -1}
	picture := base.forMotionPOC(28)
	if base.motionPOC != -1 || picture.motionPOC != 28 {
		t.Fatalf("base=%d picture=%d", base.motionPOC, picture.motionPOC)
	}
}
