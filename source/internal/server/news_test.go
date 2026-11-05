package server

import "testing"

func TestParseNewsJSONLenient(t *testing.T) {
	in := "\xEF\xBB\xBF[{\"NewsId\":\"7\",\"Subject\":\"Hi\",\"Category\":9},{\"Message\":\"x\",\"DateNumber\":1.7e9}]"
	n, err := parseNewsJSON([]byte(in))
	if err != nil || len(n) != 2 {
		t.Fatalf("%v %v", n, err)
	}
	if n[0].NewsID != 7 || n[0].Category != 3 || n[1].NewsID != 2 || n[1].DateNumber != 1700000000 {
		t.Fatalf("%+v", n)
	}
	if _, err := parseNewsJSON([]byte("[{,}]")); err == nil {
		t.Fatal("want error")
	}
}
