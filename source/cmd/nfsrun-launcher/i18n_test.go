package main

import (
	"regexp"
	"testing"
)

var verbRE = regexp.MustCompile(`%[a-z]`)

func TestTextsComplete(t *testing.T) {
	if len(texts) != 14 {
		t.Fatalf("%d languages, want the 14 of the game", len(texts))
	}
	for lang, m := range texts {
		for k, en := range texts[langEN] {
			s, ok := m[k]
			if !ok {
				t.Errorf("%s: missing %q", lang, k)
				continue
			}
			if a, b := verbRE.FindAllString(en, -1), verbRE.FindAllString(s, -1); len(a) != len(b) || (len(a) > 0 && fmtList(a) != fmtList(b)) {
				t.Errorf("%s %q: format verbs %v, English has %v", lang, k, b, a)
			}
		}
		for k := range m {
			if _, ok := texts[langEN][k]; !ok {
				t.Errorf("%s: unknown key %q", lang, k)
			}
		}
	}
}

func fmtList(v []string) string {
	s := ""
	for _, x := range v {
		s += x
	}
	return s
}

func TestLangFromLocale(t *testing.T) {
	for loc, want := range map[string]langID{
		"en_US": langEN, "pt_BR": langPT, "pt-PT": langPT, "es_ES": langES, "es": langES,
		"es_MX": langESMX, "es-AR": langESMX, "jp_JP": langJA, "ja-JP": langJA, "zh_CN": langZH,
		"ko_KR": langKO, "cs_CZ": langCS, "nl_NL": langNL, "pl_PL": langPL, "ru_RU": langRU,
		"de_DE": langDE, "fr_FR": langFR, "it_IT": langIT, "xx_YY": langEN,
	} {
		if got, _ := langFromLocale(loc); got != want {
			t.Errorf("%s -> %s, want %s", loc, got, want)
		}
	}
	defer func() { curLang = langEN }()
	setLanguage("pt_BR")
	if T("play") != "JOGAR" || T("err_name_hint", "x.ini") != "Defina um nome menor em x.ini (PLAYER_NAME=SeuNome)." {
		t.Fatalf("pt: %q %q", T("play"), T("err_name_hint", "x.ini"))
	}
}
