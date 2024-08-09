package lambroll_test

import (
	"archive/zip"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/fujiwara/lambroll"
)

type zipTestSuite struct {
	WorkingDir string
	SrcDir     string
}

func (s zipTestSuite) String() string {
	return fmt.Sprintf("%s_src_%s", s.WorkingDir, s.SrcDir)
}

var createZipArchives = []zipTestSuite{
	{".", "test/src"},
	{"test/src/dir", "../"},
}

func TestCreateZipArchive(t *testing.T) {
	for _, s := range createZipArchives {
		t.Run(s.String(), func(t *testing.T) {
			testCreateZipArchive(t, s)
		})
	}
}

func testCreateZipArchive(t *testing.T, s zipTestSuite) {
	cwd, _ := os.Getwd()
	os.Chdir(s.WorkingDir)
	defer os.Chdir(cwd)

	excludes := []string{}
	excludes = append(excludes, lambroll.DefaultExcludes...)
	excludes = append(excludes, []string{"*.bin", "skip/*"}...)
	r, info, err := lambroll.CreateZipArchive(s.SrcDir, excludes)
	if err != nil {
		t.Error("failed to CreateZipArchive", err)
	}
	defer r.Close()
	defer os.Remove(r.Name())

	zr, err := zip.OpenReader(r.Name())
	if err != nil {
		t.Error("failed to new zip reader", err)
	}
	if len(zr.File) != 6 {
		t.Errorf("unexpected included files num %d expect %d", len(zr.File), 6)
	}
	for _, f := range zr.File {
		h := f.FileHeader
		t.Logf("%s %10d %s %s",
			h.Mode(),
			h.UncompressedSize64,
			h.Modified.Format(time.RFC3339),
			h.Name,
		)
	}
	if info.Size() < 100 {
		t.Errorf("too small file got %d bytes", info.Size())
	}
}

func TestLoadZipArchive(t *testing.T) {
	r, info, err := lambroll.LoadZipArchive("test/src.zip")
	if err != nil {
		t.Error("failed to LoadZipArchive", err)
	}
	defer r.Close()

	if info.Size() < 100 {
		t.Errorf("too small file got %d bytes", info.Size())
	}
}

func TestLoadNotZipArchive(t *testing.T) {
	_, _, err := lambroll.LoadZipArchive("test/src/hello.txt")
	if err == nil {
		t.Error("must be failed to load not a zip file")
	}
	t.Log(err)
}
