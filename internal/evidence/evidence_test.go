package evidence

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

const (
	png  = "\x89PNG\r\n\x1a\n----"
	jpeg = "\xff\xd8\xff\xe0----"
	webp = "RIFF\x10\x00\x00\x00WEBPVP8 "
)

type runs struct {
	contract.NoStore
	dir string
}

func (r runs) Dir(_, run string) string { return filepath.Join(r.dir, run) }

// attempt gives a kit over a run folder of its own and the evidence folder
// of the attempt T5.1 in the run r1.
func attempt(t testing.TB) (contract.Evidence, string) {
	t.Helper()
	k := contract.NewKit()
	base := t.TempDir()
	k.Store = runs{dir: base}
	Plug(k)
	dir := filepath.Join(contract.AttemptDir(filepath.Join(base, "r1"), "T5.1"), "evidence")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return k.Evidence, dir
}

var day = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// save writes a file whose time is so many minutes into the day.
func save(t testing.TB, dir, name, body string, minute int) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	at := day.Add(time.Duration(minute) * time.Minute)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

func link(t testing.TB, to, at string) {
	t.Helper()
	if err := os.Symlink(to, at); err != nil {
		t.Skip("this system lets the test make no link:", err)
	}
}

func names(files []contract.EvidenceFile) string {
	var out []string
	for _, f := range files {
		out = append(out, f.Name+":"+f.Kind)
	}
	return strings.Join(out, " ")
}

func TestListIsOldestFirstWithSizeTimeAndKind(t *testing.T) {
	ev, dir := attempt(t)
	save(t, dir, "third.webp", webp, 3)
	save(t, dir, "first.png", png, 1)
	save(t, dir, "second.jpg", jpeg, 2)
	save(t, dir, "drawing.png", "<svg onload='x()'>", 4) // a script in an image's name
	save(t, dir, "note.txt", "ok", 5)
	save(t, dir, "empty.png", "", 6)
	save(t, dir, "b.png", png, 7)
	save(t, dir, "a.png", png, 7)
	save(t, dir, ".hidden.png", png, 0)
	save(t, dir, "two\nlines.png", png, 0)
	if err := os.Mkdir(filepath.Join(dir, "trace"), 0o700); err != nil {
		t.Fatal(err)
	}
	save(t, filepath.Join(dir, "trace"), "inner.png", png, 0)

	files, err := ev.List("", "r1", "T5.1")
	if err != nil {
		t.Fatal(err)
	}
	want := "first.png:png second.jpg:jpeg third.webp:webp drawing.png: note.txt: empty.png: a.png:png b.png:png"
	if got := names(files); got != want {
		t.Fatalf("listed\n %s\nwant\n %s", got, want)
	}
	if f := files[0]; f.Size != int64(len(png)) || !f.At.Equal(day.Add(time.Minute)) {
		t.Errorf("the first file is %d bytes at %s", f.Size, f.At)
	}
}

func TestNoFolderIsNoEvidence(t *testing.T) {
	ev, _ := attempt(t)
	for _, a := range []string{"T5.2", "T9.1"} {
		if files, err := ev.List("", "r1", a); err != nil || files != nil {
			t.Errorf("%s: %v, %v", a, files, err)
		}
		if _, _, err := ev.Open("", "r1", a, 0); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("open in %s: %v", a, err)
		}
	}
}

func TestALinkLeadsNowhere(t *testing.T) {
	ev, dir := attempt(t)
	outside := t.TempDir()
	save(t, outside, "secret.png", png+"secret", 0)
	save(t, dir, "mine.png", png, 1)
	link(t, filepath.Join(outside, "secret.png"), filepath.Join(dir, "out.png"))
	link(t, outside, filepath.Join(dir, "folder"))
	link(t, filepath.Join("..", "token"), filepath.Join(dir, "up.png"))
	link(t, "mine.png", filepath.Join(dir, "again.png"))
	save(t, filepath.Dir(dir), "token", png, 0)

	files, err := ev.List("", "r1", "T5.1")
	if err != nil || names(files) != "mine.png:png" {
		t.Fatalf("listed %q, %v", names(files), err)
	}
	if _, _, err := ev.Open("", "r1", "T5.1", 1); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a second file opened: %v", err)
	}
}

// A file that becomes a link after it was listed is the instant between a
// page's list and a browser's request for one of its numbers.
func TestAFileSwappedForALinkDoesNotOpen(t *testing.T) {
	ev, dir := attempt(t)
	outside := t.TempDir()
	save(t, outside, "secret.png", png+"secret", 0)
	save(t, dir, "mine.png", png, 1)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	files, err := listed(root)
	if err != nil || len(files) != 1 {
		t.Fatal(files, err)
	}
	if err := os.Rename(filepath.Join(dir, "mine.png"), filepath.Join(dir, "moved.png")); err != nil {
		t.Fatal(err)
	}
	link(t, filepath.Join(outside, "secret.png"), filepath.Join(dir, "mine.png"))
	if h, err := open(root, &files[0]); err == nil {
		h.Close()
		t.Error("the handle left the folder")
	}
	if got, _ := ev.List("", "r1", "T5.1"); names(got) != "moved.png:png" {
		t.Errorf("listed %q", names(got))
	}
}

func TestALinkInTheFoldersPlaceIsRefused(t *testing.T) {
	ev, dir := attempt(t)
	save(t, filepath.Dir(dir), "token", "the token", 0)
	kept := dir + ".kept"
	if err := os.Rename(dir, kept); err != nil {
		t.Fatal(err)
	}
	for i, to := range []string{".", t.TempDir()} {
		link(t, to, dir)
		if files, err := ev.List("", "r1", "T5.1"); err == nil || files != nil {
			t.Errorf("a link to %q listed %v", to, files)
		}
		if h, _, err := ev.Open("", "r1", "T5.1", 0); err == nil {
			h.Close()
			t.Errorf("a link to %q opened a file", to)
		}
		if err := os.Rename(dir, fmt.Sprint(dir, ".was", i)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTheTwoLimits(t *testing.T) {
	ev, dir := attempt(t)
	for i := range contract.EvidenceFiles + 5 {
		save(t, dir, fmt.Sprintf("shot-%02d.png", i), png, i)
	}
	files, err := ev.List("", "r1", "T5.1")
	if err != nil || len(files) != contract.EvidenceFiles || files[len(files)-1].Name != "shot-39.png" {
		t.Fatalf("%d files, the last %v: %v", len(files), files[len(files)-1], err)
	}
	if _, _, err := ev.Open("", "r1", "T5.1", contract.EvidenceFiles); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the file past the limit: %v", err)
	}

	ev, dir = attempt(t)
	for i, size := range []int64{contract.EvidenceBytes - 100, 100, 1, 1} {
		name := fmt.Sprintf("big-%d.png", i)
		save(t, dir, name, png, i)
		if err := os.Truncate(filepath.Join(dir, name), size); err != nil { // a file with a hole: no bytes are written
			t.Fatal(err)
		}
		at := day.Add(time.Duration(i) * time.Minute)
		if err := os.Chtimes(filepath.Join(dir, name), at, at); err != nil {
			t.Fatal(err)
		}
	}
	if files, err = ev.List("", "r1", "T5.1"); err != nil || names(files) != "big-0.png:png big-1.png:png" {
		t.Fatalf("listed %q: %v", names(files), err)
	}
}

func TestOpenByNumber(t *testing.T) {
	ev, dir := attempt(t)
	save(t, dir, "one.png", png+"one", 1)
	save(t, dir, "two.html", "<script>", 2)
	for i, want := range []contract.EvidenceFile{{Name: "one.png", Kind: "png"}, {Name: "two.html"}} {
		h, f, err := ev.Open("", "r1", "T5.1", i)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(h)
		h.Close()
		if f.Name != want.Name || f.Kind != want.Kind || f.Size != int64(len(body)) || err != nil {
			t.Errorf("file %d is %+v with %d bytes read: %v", i, f, len(body), err)
		}
	}
	for _, i := range []int{-1, 2, 1 << 40} {
		if _, _, err := ev.Open("", "r1", "T5.1", i); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("file %d: %v", i, err)
		}
	}
}

// FuzzOpen is the evidence lookup under hostile input: whatever run, attempt
// and number a browser sends, nothing but the attempt's own evidence opens.
func FuzzOpen(f *testing.F) {
	ev, dir := attempt(f)
	save(f, dir, "mine.png", png+"mine", 1)
	save(f, filepath.Dir(dir), "token", "secret", 0)
	save(f, filepath.Dir(filepath.Dir(dir)), "state.json", "secret", 0)
	up := ".." + string(filepath.Separator)
	f.Add("r1", "T5.1", 0)
	f.Add("r1", "T5.1", 1)
	f.Add("r1", "T5.1/..", 0)
	f.Add("r1", up+"T5.1", 0)
	f.Add("r1/attempts/T5.1/..", "T5.1", 0)
	f.Add(".", "..", 0)
	f.Add("", "", -1)
	f.Add("r1", "T5.1\x00", 0)
	f.Fuzz(func(t *testing.T, run, attempt string, index int) {
		for _, list := range []bool{true, false} {
			var got string
			if list {
				files, _ := ev.List("", run, attempt)
				got = names(files)
			} else if h, file, err := ev.Open("", run, attempt, index); err == nil {
				body, _ := io.ReadAll(h)
				h.Close()
				got = file.Name + ":" + file.Kind
				if string(body) != png+"mine" {
					t.Fatalf("%q %q %d opened %q", run, attempt, index, body)
				}
			}
			if got != "" && (got != "mine.png:png" || run != "r1" || attempt != "T5.1") {
				t.Fatalf("%q %q %d gave %q", run, attempt, index, got)
			}
		}
	})
}

func TestASlotThatOpensIsSeen(t *testing.T) {
	ev, _ := attempt(t)
	held, err := net.Listen("tcp", net.JoinHostPort(contract.Loopback, "0"))
	if err != nil {
		t.Fatal(err)
	}
	slot := held.Addr().(*net.TCPAddr).Port
	other, err := net.Listen("tcp", net.JoinHostPort(contract.Loopback, "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	live := other.Addr().(*net.TCPAddr).Port
	held.Close()

	began := time.Now()
	if got := ev.Answering([]int{slot, live, 0, -1, 1 << 20}); len(got) != 1 || got[0] != live {
		t.Fatalf("with the slot closed: %v", got)
	}
	if took := time.Since(began); took > time.Second {
		t.Errorf("asking took %s", took)
	}

	opened := make(chan time.Time, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		site, err := net.Listen("tcp", net.JoinHostPort(contract.Loopback, fmt.Sprint(slot)))
		if err != nil {
			close(opened)
			return
		}
		opened <- time.Now()
		t.Cleanup(func() { site.Close() })
	}()
	// A screen asks again each time it is drawn; a second apart here.
	for range 8 {
		if got := ev.Answering([]int{live, slot}); len(got) == 2 {
			at, ok := <-opened
			if !ok {
				t.Skip("another program took the port between the two listens")
			}
			if seen := time.Since(at); got[0] != live || got[1] != slot || seen > 5*time.Second {
				t.Errorf("seen as %v after %s", got, seen)
			}
			return
		}
		time.Sleep(time.Second)
	}
	select {
	case _, ok := <-opened:
		if !ok {
			t.Skip("another program took the port between the two listens")
		}
	default:
	}
	t.Fatal("the slot opened and was never seen")
}
