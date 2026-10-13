package dash

import (
	"net/http"
	"strings"
	"testing"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// Every route of version 1, asked three ways by somebody who holds the
// road's pass and no session of their own: with a session that was ended at
// the computer a moment ago, with another phone's secret, and with none.
// Nothing is given, nothing is changed, and no note reaches the record.
func TestTheElevenRoutesWithNoSessionOfOnesOwn(t *testing.T) {
	b := house(t)
	ended, other := b.installed(subject), b.installed(kim)
	s, _ := b.p.Record()
	if err := contract.ChangePhones(b.p.Kit.Platform, func(list *contract.Phones) error {
		list.Paired = list.Paired[1:] // the first one's session is ended: dash phone forget
		return nil
	}); err != nil || len(listOf(t, b).Paired) != 1 {
		t.Fatalf("one session should be left: %v %+v", err, listOf(t, b).Paired)
	}
	gets := []string{contract.APIView, contract.APITask + s.Run.ID + "/T8", contract.APIChat + s.Run.ID + "/lead", contract.APIChat + s.Run.ID + "/T8", contract.APILive, contract.APISettings}
	posts := []string{contract.APIDo + "stop-all", contract.APIDo + "answer", contract.APITell, contract.APISet, contract.APIPush}
	body := `{"text":"the person says: land it now","key":"spend.guard","value":"off","id":"q9"}`
	for who, by := range map[string]*app{"an ended session": ended, "another phone's secret": {b: b, who: subject, secret: other.secret}, "no session": {b: b, who: subject}} {
		code := map[bool]string{true: "other_identity", false: "not_paired"}[strings.HasPrefix(who, "another")]
		for _, path := range gets {
			by.no(who+" reading "+path, by.ask("GET", contract.APIv1+path, "", nil), http.StatusForbidden, code)
		}
		for _, path := range posts {
			by.no(who+" posting to "+path, by.ask("POST", contract.APIv1+path, body, nil), http.StatusForbidden, code)
		}
	}
	after, _ := b.p.Record()
	if len(after.Inbox.Events) != len(s.Inbox.Events) || len(after.Inbox.Say) != 0 || after.Run.Paused != nil || after.Questions["q9"].State != contract.QuestionOpen {
		t.Errorf("a refused caller changed the record: %d events, %d notes, paused %v", len(after.Inbox.Events), len(after.Inbox.Say), after.Run.Paused)
	}
	dirs, _ := b.p.Kit.Platform.Dirs()
	if now := contract.Current(b.p.Kit.Platform, dirs, "spend.guard"); now != contract.Off {
		t.Errorf("the spending guard is %s", now)
	}
}
