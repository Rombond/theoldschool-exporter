package main

import "testing"

func TestParseSize(t *testing.T) {
	cases := map[string]float64{
		"511.96 GiB":   511.96 * 1024 * 1024 * 1024,
		"78.13 GiB":    78.13 * 1024 * 1024 * 1024,
		"1,024.50 MiB": 1024.5 * 1024 * 1024,
		"0 B":          0,
	}
	for in, want := range cases {
		got, err := parseSize(in)
		if err != nil || got != want {
			t.Errorf("parseSize(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
}

func TestExtract(t *testing.T) {
	page := `<li class="ratio-bar__uploaded" title="Uploader"><a href="x"><i></i> 511.96 GiB </a></li>`
	got, err := extractBytes(uploadRe, page)
	if err != nil || got != 511.96*1024*1024*1024 {
		t.Fatalf("got %v %v", got, err)
	}
}
