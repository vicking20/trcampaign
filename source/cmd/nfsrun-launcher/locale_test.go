package main

import "testing"

func TestGameLocale(t *testing.T) {
	for in, want := range map[string]string{
		"ru_RU": "ru_RU", "ru-ru": "ru_RU", "en_US": "en_US", "es_MX": "es_MX", "es-AR": "es_MX",
		"es_ES": "es_ES", "fr-CA": "fr_FR", "de_AT": "de_DE", "zh_CN": "zh_TW", "cs_CZ": "cs_CZ",
		"ja_JP": "ja_JP", "en_GB": "en_UK",
	} {
		if got, ok := gameLocale(in); !ok || got != want {
			t.Errorf("%s -> %s %v, want %s", in, got, ok, want)
		}
	}
	for _, in := range []string{"pt_BR", "pt_PT", "ko_KR", "tr_TR", ""} {
		if got, ok := gameLocale(in); ok || got != "en_US" {
			t.Errorf("%s -> %s %v, want en_US false", in, got, ok)
		}
	}
}
