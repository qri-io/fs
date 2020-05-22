package qfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAbsPath(t *testing.T) {
	tmp, err := filepath.Abs(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	pathAbs, err := filepath.Abs("relative/path/data.yaml")
	if err != nil {
		t.Fatal(err)
	}

	httpAbs, err := filepath.Abs("http_got/relative/dataset.yaml")
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		in, out, err string
	}{
		{"", "", ""},
		{"http://example.com/zipfile.zip", "http://example.com/zipfile.zip", ""},
		{"https://example.com/zipfile.zip", "https://example.com/zipfile.zip", ""},
		{"relative/path/data.yaml", pathAbs, ""},
		{"http_got/relative/dataset.yaml", httpAbs, ""},
		{"/ipfs", "/ipfs", ""},
		{tmp, tmp, ""},
	}

	for i, c := range cases {
		got := c.in
		err := AbsPath(&got)
		if !(err == nil && c.err == "" || (err != nil && c.err == err.Error())) {
			t.Errorf("case %d error mismatch. expected: %s, got: %s", i, c.err, err)
		}
		if got != c.out {
			t.Errorf("case %d error mismatch. expected: %s, got: %s", i, c.out, got)
		}
	}
}

func TestPathKind(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"", "none"},
		{"http://example", "http"},
		{"https://example", "http"},
		{"/path/to/location", "local"},
		{"/", "local"},
		{"/ipfs/Qmfoo", "ipfs"},
		{"/mem/Qmfoo", "mem"},
		{"/map/Qmfoo", "map"},
	}

	for i, c := range cases {
		got := PathKind(c.in)
		if got != c.out {
			t.Errorf("case %d: expected: %s, got :%s", i, c.out, got)
		}
	}
}
