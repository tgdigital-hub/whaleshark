// Package evidence lists and opens what a worker saved from its browser
// check, and says which ports a site answers on. It starts no browser, binds
// no command and reads no record.
package evidence

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/cli"
	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Plug binds this package's handlers and puts its implementations into the kit.
func Plug(k *contract.Kit) { k.Evidence = evidence{k} }

type evidence struct{ k *contract.Kit }

// folder opens an attempt's evidence folder, nil where there is none. The
// two ids pass the validator before they become part of a path, and a link
// standing where the folder should be is refused: it could lead to the
// attempt's own files, its token among them.
func (e evidence) folder(root, run, attempt string) (*os.Root, error) {
	if err := cmp.Or(cli.Valid("id", run, ""), cli.Valid("attempt", attempt, "")); err != nil {
		return nil, err
	}
	at, err := os.OpenRoot(contract.AttemptDir(e.k.Reader().Dir(root, run), attempt))
	if err != nil {
		return nil, gone(err)
	}
	defer at.Close()
	if fi, err := at.Lstat("evidence"); err != nil {
		return nil, gone(err)
	} else if !fi.IsDir() {
		return nil, fmt.Errorf("the evidence folder of %s is no folder", attempt)
	}
	return at.OpenRoot("evidence")
}

func gone(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// list is the folder's own files, oldest first and by name within one
// instant, up to the two limits. A link, a folder, a hidden file and a name
// that is not one harmless line are no evidence. Kinds are not judged here.
func list(dir *os.Root) ([]contract.EvidenceFile, error) {
	d, err := dir.Open(".")
	if err != nil {
		return nil, err
	}
	defer d.Close()
	names, err := d.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	var all []contract.EvidenceFile
	for _, name := range names {
		fi, err := dir.Lstat(name)
		if err != nil || !fi.Mode().IsRegular() || strings.HasPrefix(name, ".") || cli.Line(name) != name {
			continue
		}
		all = append(all, contract.EvidenceFile{Name: name, Size: fi.Size(), At: fi.ModTime()})
	}
	slices.SortFunc(all, func(a, b contract.EvidenceFile) int {
		return cmp.Or(a.At.Compare(b.At), strings.Compare(a.Name, b.Name))
	})
	var total int64
	for i, f := range all {
		if total += f.Size; i == contract.EvidenceFiles || total > contract.EvidenceBytes {
			return all[:i], nil
		}
	}
	return all, nil
}

// open opens one listed file and judges it by the handle it returns, so
// what a caller reads is what was judged, whatever the folder holds by now.
func open(dir *os.Root, f *contract.EvidenceFile) (*os.File, error) {
	h, err := dir.Open(f.Name)
	if err != nil {
		return nil, err
	}
	fi, err := h.Stat()
	if err == nil && !fi.Mode().IsRegular() {
		err = fmt.Errorf("%s is no file", f.Name)
	}
	if err != nil {
		h.Close()
		return nil, err
	}
	head := make([]byte, 12)
	n, _ := h.ReadAt(head, 0)
	f.Size, f.At, f.Kind = fi.Size(), fi.ModTime(), kind(head[:n])
	return h, nil
}

// kind names an image by the first bytes its format's own specification
// gives it: PNG's signature, JPEG's start-of-image marker, WebP's RIFF
// container. Everything else has none, an SVG among it.
func kind(head []byte) string {
	switch {
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case bytes.HasPrefix(head, []byte("\xff\xd8\xff")):
		return "jpeg"
	case len(head) == 12 && string(head[:4]) == "RIFF" && string(head[8:]) == "WEBP":
		return "webp"
	}
	return ""
}

// listed is list with each file's kind, less what cannot be opened.
func listed(dir *os.Root) ([]contract.EvidenceFile, error) {
	files, err := list(dir)
	out := files[:0]
	for _, f := range files {
		if h, err := open(dir, &f); err == nil {
			h.Close()
			out = append(out, f)
		}
	}
	return out, err
}

func (e evidence) List(root, run, attempt string) ([]contract.EvidenceFile, error) {
	dir, err := e.folder(root, run, attempt)
	if dir == nil {
		return nil, err
	}
	defer dir.Close()
	return listed(dir)
}

func (e evidence) Open(root, run, attempt string, index int) (io.ReadCloser, contract.EvidenceFile, error) {
	var files []contract.EvidenceFile
	dir, err := e.folder(root, run, attempt)
	if dir != nil {
		defer dir.Close()
		files, err = listed(dir)
	}
	if err == nil && (index < 0 || index >= len(files)) {
		err = fmt.Errorf("%s has no evidence file %d: %w", attempt, index, fs.ErrNotExist)
	}
	if err != nil {
		return nil, contract.EvidenceFile{}, err
	}
	f := files[index]
	h, err := open(dir, &f)
	if err != nil {
		return nil, contract.EvidenceFile{}, err
	}
	return h, f, nil
}

// Answering tries every port at once on the loopback address and gives all
// of them together a fifth of a second: a closed port is refused at once on
// most systems, and one that says nothing must not hold a screen up.
func (evidence) Answering(ports []int) []int {
	dial := net.Dialer{Deadline: time.Now().Add(time.Second / 5)}
	up := make([]bool, len(ports))
	var wg sync.WaitGroup
	for i, port := range ports {
		wg.Go(func() {
			if c, err := dial.Dial("tcp", net.JoinHostPort(contract.Loopback, strconv.Itoa(port))); err == nil {
				c.Close()
				up[i] = true
			}
		})
	}
	wg.Wait()
	var out []int
	for i, port := range ports {
		if up[i] {
			out = append(out, port)
		}
	}
	return out
}
