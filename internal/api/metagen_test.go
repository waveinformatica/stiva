package api

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
	"testing"

	"registry/internal/registry"
)

type fakeBackend struct {
	objs map[string][]byte
}

func (f *fakeBackend) Name() string { return "fake" }
func (f *fakeBackend) Put(p, ct string, r io.Reader) error {
	b, _ := io.ReadAll(r)
	f.objs[p] = b
	return nil
}
func (f *fakeBackend) Get(p string) (io.ReadCloser, int64, string, error) {
	b, ok := f.objs[p]
	if !ok {
		return nil, 0, "", registry.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), "", nil
}
func (f *fakeBackend) Head(p string) (int64, string, error) {
	b, ok := f.objs[p]
	if !ok {
		return 0, "", registry.ErrNotFound
	}
	return int64(len(b)), "", nil
}
func (f *fakeBackend) Close() error { return nil }
func (f *fakeBackend) List(prefix string) ([]string, error) {
	var out []string
	for k := range f.objs {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	return out, nil
}
func (f *fakeBackend) Delete(p string) error {
	delete(f.objs, p)
	return nil
}

func buildTestDeb() ([]byte, error) {
	ctrl := "Package: mypkg\nVersion: 1.0\nArchitecture: amd64\nMaintainer: t@t\nDescription: test\n"
	buf := &bytes.Buffer{}
	gw := gzip.NewWriter(buf)
	tw := tar.NewWriter(gw)
	if err := tw.WriteHeader(&tar.Header{Name: "control", Mode: 0o644, Size: int64(len(ctrl))}); err != nil {
		return nil, err
	}
	if _, err := tw.Write([]byte(ctrl)); err != nil {
		return nil, err
	}
	tw.Close()
	gw.Close()
	control := buf.Bytes()

	arMember := func(name string, data []byte) []byte {
		nm := []byte(name)
		if len(nm) < 16 {
			nm = append(nm, bytes.Repeat([]byte("/"), 16-len(nm))...)
		} else {
			nm = nm[:16]
		}
		hdr := append(append(append(append(append(append(append([]byte{}, nm...),
			[]byte("0           ")...), []byte("0     ")...), []byte("0     ")...),
			[]byte("100644  ")...), []byte(fmt.Sprintf("%10d", len(data)))...), []byte("`\n")...)
		out := append(hdr, data...)
		if len(data)%2 == 1 {
			out = append(out, '\n')
		}
		return out
	}

	var out bytes.Buffer
	out.WriteString("!<arch>\n")
	out.Write(arMember("debian-binary", []byte("2.0\n")))
	out.Write(arMember("control.tar.gz", control))
	out.Write(arMember("data.tar.gz", gzipCompress([]byte("x"))))
	return out.Bytes(), nil
}

func gzipCompress(b []byte) []byte {
	buf := &bytes.Buffer{}
	gw := gzip.NewWriter(buf)
	gw.Write(b)
	gw.Close()
	return buf.Bytes()
}

func TestGenAPTDeb(t *testing.T) {
	deb, err := buildTestDeb()
	if err != nil {
		t.Fatal(err)
	}
	be := &fakeBackend{objs: map[string][]byte{"pool/main/m/mypkg/mypkg_1.0_amd64.deb": deb}}
	body, ct, ok := genAPT(be, "Packages")
	if !ok {
		t.Fatal("genAPT not handled")
	}
	t.Logf("content-type=%s\nbody=%s", ct, body)
	if !strings.Contains(string(body), "Package: mypkg") {
		t.Fatalf("missing package stanza: %s", body)
	}
}
