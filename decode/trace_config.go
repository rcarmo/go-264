package decode

import (
	"os"
	"strconv"
)

type traceFlag uint64

const (
	traceBCABAC traceFlag = 1 << iota
	traceBMB
	traceBMVDComp
	traceBMVD
	traceBMVP
	traceBRef
	traceBResidual
	traceBState
	traceBType
	traceCABACCBP
	traceCABACSyntax
	traceCABACTerminate
	traceDirectCol
	traceDirectCtx
	traceDirect
	disableDeblock
	traceHeader
	traceMotionSaveDetail
	traceMotionSave
	traceMotionWrite
	tracePBin
	tracePCABAC
	tracePMVDComp
	tracePMVPCandidate
	tracePMVPEnabled
	tracePRef
	tracePState
	tracePType
	traceRecon
	traceRefList
	traceTemporalDirect
)

type traceConfig struct {
	flags traceFlag

	pTypeLimit      int
	bTypeLimit      int
	bTypePOC        int
	pMVPPOC         int
	pMVPFromMB      int
	pMVPToMB        int
	temporalPOC     int
	motionSaveLimit int
	motionPOC       int
}

func snapshotTraceConfig() traceConfig {
	c := traceConfig{
		pTypeLimit: 2, bTypeLimit: 20, bTypePOC: 20,
		pMVPPOC: 28, pMVPFromMB: 0, pMVPToMB: 24,
		temporalPOC: 20, motionSaveLimit: -1, motionPOC: -1,
	}
	for _, item := range []struct {
		name string
		flag traceFlag
	}{
		{"GO264_B_CABAC_TRACE", traceBCABAC},
		{"GO264_B_MB_TRACE", traceBMB},
		{"GO264_B_MVD_COMP_TRACE", traceBMVDComp},
		{"GO264_B_MVD_TRACE", traceBMVD},
		{"GO264_B_MVP_TRACE", traceBMVP},
		{"GO264_B_REF_TRACE", traceBRef},
		{"GO264_B_RESIDUAL_TRACE", traceBResidual},
		{"GO264_B_STATE_TRACE", traceBState},
		{"GO264_B_TYPE_TRACE", traceBType},
		{"GO264_CABAC_CBP_TRACE", traceCABACCBP},
		{"GO264_CABAC_SYNTAX_TRACE", traceCABACSyntax},
		{"GO264_CABAC_TERMINATE_TRACE", traceCABACTerminate},
		{"GO264_DIRECT_COL_TRACE", traceDirectCol},
		{"GO264_DIRECT_CTX_TRACE", traceDirectCtx},
		{"GO264_DIRECT_TRACE", traceDirect},
		{"GO264_DISABLE_DEBLOCK", disableDeblock},
		{"GO264_HEADER_TRACE", traceHeader},
		{"GO264_MOTION_SAVE_DETAIL", traceMotionSaveDetail},
		{"GO264_MOTION_SAVE_TRACE", traceMotionSave},
		{"GO264_MOTION_WRITE_TRACE", traceMotionWrite},
		{"GO264_P_BIN_TRACE", tracePBin},
		{"GO264_P_CABAC_TRACE", tracePCABAC},
		{"GO264_P_MVD_COMP_TRACE", tracePMVDComp},
		{"GO264_P_MVP_CAND_TRACE", tracePMVPCandidate},
		{"GO264_P_MVP_TRACE", tracePMVPEnabled},
		{"GO264_P_REF_TRACE", tracePRef},
		{"GO264_P_STATE_TRACE", tracePState},
		{"GO264_P_TYPE_TRACE", tracePType},
		{"GO264_RECON_TRACE", traceRecon},
		{"GO264_REF_LIST_TRACE", traceRefList},
		{"GO264_TEMPORAL_DIRECT_TRACE", traceTemporalDirect},
	} {
		if os.Getenv(item.name) != "" {
			c.flags |= item.flag
		}
	}
	c.pTypeLimit = positiveEnvInt("GO264_P_TYPE_TRACE_LIMIT", c.pTypeLimit)
	c.bTypeLimit = positiveEnvInt("GO264_B_TYPE_TRACE_LIMIT", c.bTypeLimit)
	c.bTypePOC = envIntSnapshot("GO264_B_TYPE_TRACE_POC", c.bTypePOC)
	c.pMVPPOC = envIntSnapshot("GO264_P_MVP_TRACE_POC", c.pMVPPOC)
	c.pMVPFromMB = envIntSnapshot("GO264_P_MVP_TRACE_FROM_MB", c.pMVPFromMB)
	c.pMVPToMB = envIntSnapshot("GO264_P_MVP_TRACE_TO_MB", c.pMVPToMB)
	c.temporalPOC = envIntSnapshot("GO264_TEMPORAL_DIRECT_TRACE_POC", c.temporalPOC)
	c.motionSaveLimit = envIntSnapshot("GO264_MOTION_SAVE_MB_LIMIT", c.motionSaveLimit)
	return c
}

func (c *traceConfig) enabled(flag traceFlag) bool {
	return c != nil && c.flags&flag != 0
}

func (c traceConfig) forMotionPOC(poc int) traceConfig {
	c.motionPOC = poc
	return c
}

func firstTraceConfig(configs []*traceConfig) *traceConfig {
	if len(configs) != 0 && configs[0] != nil {
		return configs[0]
	}
	trace := snapshotTraceConfig()
	return &trace
}

func envIntSnapshot(name string, fallback int) int {
	if value := os.Getenv(name); value != "" {
		if n, err := strconv.Atoi(value); err == nil {
			return n
		}
	}
	return fallback
}

func positiveEnvInt(name string, fallback int) int {
	n := envIntSnapshot(name, fallback)
	if n > 0 {
		return n
	}
	return fallback
}
