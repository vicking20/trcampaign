package main

import (
	"log"
	"runtime"
	"strings"
)

// The PC game knows these Locale values (0x01887CC0, case-insensitive);
// anything else is English. Portuguese and Korean text exist only on
// consoles; the PC's Chinese is Traditional (zh_TW).
var gameLocales = map[string]string{
	"en_us": "en_US", "en_gb": "en_UK", "en_uk": "en_UK",
	"fr_fr": "fr_FR", "de_de": "de_DE", "es_es": "es_ES", "es_mx": "es_MX",
	"it_it": "it_IT", "ja_jp": "ja_JP", "ru_ru": "ru_RU", "pl_pl": "pl_PL",
	"nl_nl": "nl_NL", "zh_tw": "zh_TW", "zh_cn": "zh_TW", "cs_cz": "cs_CZ",
}

// gameLocale maps a LOCALE value (en_US, pt-BR, ...) to the game's Locale.
// ok = false: the game has no text in that language (English is used).
func gameLocale(loc string) (code string, ok bool) {
	k := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(loc), "-", "_"))
	if c, found := gameLocales[k]; found {
		return c, true
	}
	// another country (es_AR, fr_CA, de_AT, ...) -> that language
	lang, country, _ := strings.Cut(k, "_")
	if lang == "es" && country != "" && country != "es" {
		return "es_MX", true // American Spanish
	}
	if c, found := langDefault[lang]; found {
		return c, true
	}
	return "en_US", false
}

var langDefault = map[string]string{
	"en": "en_US", "fr": "fr_FR", "de": "de_DE", "es": "es_ES", "it": "it_IT",
	"ja": "ja_JP", "ru": "ru_RU", "pl": "pl_PL", "nl": "nl_NL", "cs": "cs_CZ",
	"zh": "zh_TW",
}

const gameRegKey = `Software\EA Games\Need for Speed(TM) The Run`

// setGameLocale writes the game's Locale registry value. The launcher's game
// copy reads HKCU (no administrator needed); the original exe reads HKLM.
func setGameLocale(l *log.Logger, code string, hkcu bool) {
	if runtime.GOOS != "windows" {
		return
	}
	where := `HKCU\` + gameRegKey
	if !hkcu {
		where = `HKLM\` + gameRegKey
	}
	if err := regSetString(!hkcu, gameRegKey, "Locale", code); err != nil {
		l.Printf("[LOCALE] could not set the game language (%s\\Locale = %s): %v", where, code, err)
		if !hkcu {
			l.Printf("[LOCALE] the original game exe reads it from HKLM: run the launcher once as administrator, or use NO_ORIGIN_START=1 (the launcher's game copy reads HKCU)")
		}
		return
	}
	l.Printf("[LOCALE] game language %s (%s\\Locale)", code, where)
}
