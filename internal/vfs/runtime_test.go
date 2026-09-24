package vfs

import (
	"testing"

	"github.com/AngerLab/gotrackfs/internal/hostfs"

	"github.com/winfsp/cgofuse/fuse"
	"golang.org/x/text/unicode/norm"
)

// The runtime read path is FS-injectable: openReal goes through Options.FS,
// so MemFS exercises serving plain files exactly like the disk would.

func TestOpenReal_ServesPlainFileFromInjectedFS(t *testing.T) {
	mem := hostfs.NewMem().AddFile("cover.jpg", []byte("jpeg-bytes"))
	v := New(Options{FS: mem})
	defer v.Destroy()

	rc, fh := v.openReal("/cover.jpg")
	if rc != 0 {
		t.Fatalf("openReal rc = %d, want 0", rc)
	}
	defer v.Release("", fh)

	buf := make([]byte, 16)
	if n := v.Read("/cover.jpg", buf, 0, fh); n != len("jpeg-bytes") {
		t.Fatalf("Read = %d, want %d", n, len("jpeg-bytes"))
	}
	if got := string(buf[:len("jpeg-bytes")]); got != "jpeg-bytes" {
		t.Errorf("read %q, want %q", got, "jpeg-bytes")
	}

	// Offset read through the same handle.
	off := make([]byte, 4)
	if n := v.Read("/cover.jpg", off, 5, fh); n != 4 {
		t.Fatalf("offset Read = %d, want 4", n)
	}
	if got := string(off); got != "byte" {
		t.Errorf("offset read %q, want %q", got, "byte")
	}
}

func TestOpenReal_ErrorMapping(t *testing.T) {
	mem := hostfs.NewMem().AddDir("dir").AddFile("f.bin", nil)
	v := New(Options{FS: mem})
	defer v.Destroy()

	if rc, _ := v.openReal("/nope"); rc != -fuse.ENOENT {
		t.Errorf("missing file rc = %d, want ENOENT", rc)
	}
	if rc, _ := v.openReal("/dir"); rc != -fuse.EISDIR {
		t.Errorf("directory rc = %d, want EISDIR", rc)
	}
	if rc, _ := v.openReal("/f.bin"); rc != 0 {
		t.Errorf("plain file rc = %d, want 0", rc)
	}
}

// TestOpenReal_NFDFallback pins that runtime serving inherits the NFC/NFD
// retry from the FS decorator (previously duplicated in openRealFile).
func TestOpenReal_NFDFallback(t *testing.T) {
	mem := hostfs.NewMem().AddFile(norm.NFD.String("Épisode 1/flac"), []byte("audio"))
	v := New(Options{FS: hostfs.WithNFDFallback(mem)})
	defer v.Destroy()

	rc, fh := v.openReal(norm.NFC.String("Épisode 1") + "/flac")
	if rc != 0 {
		t.Fatalf("openReal(NFC query) rc = %d, want 0 via NFD retry", rc)
	}
	defer v.Release("", fh)

	buf := make([]byte, 5)
	if n := v.Read("/", buf, 0, fh); n != 5 {
		t.Fatalf("Read = %d, want 5", n)
	}
	if got := string(buf); got != "audio" {
		t.Errorf("read %q, want %q", got, "audio")
	}
}
