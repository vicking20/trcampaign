package server

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"nfsrun-server/internal/blaze"
)

// Util::fetchClientConfig (0x0009/0x0001).
//
// The client asks for named sections (CFID="NFS12_THERUN"). The reply is
// FetchConfigResponse { CONF map<string,string> }. Sections are plain files so
// they can be edited without rebuilding:
//
//	client_config/NFS12_THERUN.cfg
//	  # comment
//	  key = value
//
// A missing file gives an empty map.

var sectionName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

func loadClientConfig(dir, section string) (map[string]string, string, error) {
	if !sectionName.MatchString(section) {
		return nil, "", nil
	}
	path := filepath.Join(dir, section+".cfg")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, path, nil
		}
		return nil, path, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\uFEFF"))
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if k = strings.TrimSpace(k); k != "" {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out, path, sc.Err()
}

func buildFetchConfigResponse(conf map[string]string) []byte {
	var w blaze.Writer
	keys := make([]string, 0, len(conf))
	for k := range conf {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	writeStringMap(&w, "CONF", keys, conf)
	return w.Bytes()
}
