package server

import "testing"

func TestCarYearOf(t *testing.T) {
	for id, want := range map[string]int{
		"AST_ONE_77_10_PP_STOCK_1": 10, "FOR_MUS_302_69_NFS_E": 69, "BMW_M3_E30_90_RARE": 90,
		"NIS_S14_A_97_ADSALES_1": 97, "LAM_COU_QV_85_PP1V1": 85, "MCL_MP4_12C_12_PP_RARE_1": 12,
	} {
		if got, ok := carYearOf(id); !ok || got != want {
			t.Errorf("%s: got %d %v, want %d", id, got, ok, want)
		}
	}
}
