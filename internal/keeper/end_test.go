package keeper

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// The end of a connection is found by both sides, however it is told. On
// Windows a read that is waiting when the other side closes is, about once
// in five hundred, never told (measured on a hosted machine, with a bare
// listener as with the keeper), so on that system this many connections
// meet the case several times: before contract.Patient the first half left
// a reader waiting for ever and the second left the keeper holding
// connections nobody was on.
func TestTheEndOfAConnectionIsFoundByBothSides(t *testing.T) {
	r := newRig(t)
	path := os.Getenv(contract.EnvSocket)
	each := func(n int, one func(conn net.Conn)) {
		var left atomic.Int64
		left.Store(int64(n))
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				for left.Add(-1) >= 0 && !t.Failed() {
					conn, err := net.Dial("unix", path)
					if err != nil {
						t.Error(err)
						return
					}
					one(conn)
					conn.Close()
				}
			})
		}
		wg.Wait()
	}
	say := func(conn net.Conn, call contract.WireCall) (r contract.WireReply) {
		body, _ := json.Marshal(call)
		err := contract.WriteFrame(conn, contract.FrameCall, body)
		if err == nil {
			_, body, err = contract.ReadFrame(conn)
		}
		if err != nil || json.Unmarshal(body, &r) != nil {
			t.Errorf("%s was answered with %v", call.Op, err)
		}
		return r
	}
	// The keeper closes: a hello of another version is answered and ended.
	var late atomic.Int64
	each(1500, func(conn net.Conn) {
		conn = contract.Patient(conn)
		conn.SetDeadline(time.Now().Add(20 * time.Second))
		say(conn, contract.WireCall{V: contract.WireVersion + 1, Op: contract.OpHello})
		began := time.Now()
		if _, _, err := contract.ReadFrame(conn); !errors.Is(err, io.EOF) {
			t.Errorf("the end of a connection the keeper closed was read as %v", err)
		}
		if time.Since(began) > contract.EndWait/2 {
			late.Add(1)
		}
	})
	// The other side closes, as every command does after its answer.
	each(1500, func(conn net.Conn) {
		say(conn, contract.WireCall{V: contract.WireVersion, Op: contract.OpHello})
		say(conn, contract.WireCall{Op: contract.OpVersion})
	})
	r.until("the keeper to let go of every connection that was closed", func() bool {
		return r.locked(func() bool { return len(r.k.conns) == 0 })
	})
	t.Logf("%d of 1500 ends were found by asking again", late.Load())
}
