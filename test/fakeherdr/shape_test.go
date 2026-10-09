package fakeherdr

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
	"github.com/tgdigital-hub/whaleshark/internal/contract/testkit"
	"github.com/tgdigital-hub/whaleshark/internal/herdr"
)

// The tests in this file need the real herdr on the PATH and make only
// read-only calls to it: its bundled schema, its status and its snapshot.
// They are skipped where there is no herdr.
func realHerdr(t *testing.T, args ...string) []byte {
	t.Helper()
	out, err := exec.Command("herdr", args...).Output()
	if err != nil {
		t.Skipf("no real herdr to compare with: herdr %s: %v", strings.Join(args, " "), err)
	}
	return out
}

// busy is a fake with something of every kind in it: two tabs, a split, an
// agent with a name and a session, a bare shell.
func busy(t *testing.T) (f *Fake, lead, worker, side contract.Pane) {
	f = start(t)
	lead = panes(t, f)[0]
	worker, _ = f.TabCreate("/work/shop", "url parser", []string{"A=b"})
	f.AgentStart("h1-r1-t1-1", "claude", worker.ID, nil, time.Minute)
	side, _ = f.Split(lead.ID, "right", 0.7)
	return f, lead, worker, side
}

// problems checks a value against a node of herdr's JSON schema, and more
// strictly than JSON schema does: a key the schema does not name is a
// problem, because the fake must invent nothing.
func problems(root, node map[string]any, v any, path string) []string {
	if ref, ok := node["$ref"].(string); ok {
		at := any(root)
		for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
			at = at.(map[string]any)[part]
		}
		return problems(root, at.(map[string]any), v, path)
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if list, ok := node[key].([]any); ok {
			best := []string{path + ": no alternative of the schema"}
			for i, alt := range list {
				found := problems(root, alt.(map[string]any), v, path)
				if len(found) == 0 {
					return nil
				}
				if i == 0 || len(found) < len(best) {
					best = found
				}
			}
			return best
		}
	}
	var out []string
	if want, ok := node["const"]; ok && want != v {
		return []string{fmt.Sprintf("%s: %v, want %v", path, v, want)}
	}
	if list, ok := node["enum"].([]any); ok && !slices.Contains(list, v) {
		return []string{fmt.Sprintf("%s: %v is not one of %v", path, v, list)}
	}
	if want, ok := node["type"]; ok {
		kinds, _ := want.([]any)
		if s, ok := want.(string); ok {
			kinds = []any{s}
		}
		is := map[string]bool{}
		switch x := v.(type) {
		case nil:
			is["null"] = true
		case bool:
			is["boolean"] = true
		case string:
			is["string"] = true
		case float64:
			is["number"], is["integer"] = true, x == float64(int64(x))
		case []any:
			is["array"] = true
		case map[string]any:
			is["object"] = true
		}
		if !slices.ContainsFunc(kinds, func(k any) bool { return is[k.(string)] }) {
			return []string{fmt.Sprintf("%s: %T, want %v", path, v, want)}
		}
	}
	switch x := v.(type) {
	case []any:
		if items, ok := node["items"].(map[string]any); ok {
			for i, each := range x {
				out = append(out, problems(root, items, each, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	case map[string]any:
		props, _ := node["properties"].(map[string]any)
		for _, name := range node["required"].([]any) {
			if _, ok := x[name.(string)]; !ok {
				out = append(out, path+"."+name.(string)+": missing")
			}
		}
		for name, each := range x {
			if prop, ok := props[name].(map[string]any); ok {
				out = append(out, problems(root, prop, each, path+"."+name)...)
			} else if node["additionalProperties"] == nil {
				out = append(out, path+"."+name+": not in herdr's schema")
			}
		}
	}
	return out
}

func TestEveryAnswerAndEventLineFitsHerdrsSchema(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal(realHerdr(t, "api", "schema", "--json"), &root); err != nil {
		t.Fatal(err)
	}
	schema := func(name string) map[string]any { return root["schemas"].(map[string]any)[name].(map[string]any) }
	check := func(what, name string, line []byte) {
		t.Helper()
		var v any
		if err := json.Unmarshal(line, &v); err != nil {
			t.Errorf("%s: %v: %s", what, err, line)
			return
		}
		for _, p := range problems(root, schema(name), v, "") {
			t.Errorf("%s: %s", what, p)
		}
	}

	var wrong any
	json.Unmarshal([]byte(`{"id":"x","result":{"type":"tab_info","tab":{"tab_id":5,"made_up":1}}}`), &wrong)
	if found := problems(root, schema("success_response"), wrong, ""); len(found) == 0 {
		t.Fatal("the check against the schema finds nothing wrong with a wrong answer")
	}

	f, lead, worker, side := busy(t)
	conn, err := net.Dial("unix", f.events.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, `{"id":"a","method":"events.subscribe","params":{"subscriptions":[{"type":"pane.created"},{"type":"pane.closed"},{"type":"pane.updated"},{"type":"pane.focused"},{"type":"pane.agent_detected"},{"type":"tab.closed"},{"type":"tab.renamed"},{"type":"pane.agent_status_changed","pane_id":%q}]}}`+"\n", worker.ID)
	lines := bufio.NewReader(conn)
	line, _ := lines.ReadBytes('\n')
	check("the answer to a subscription", "success_response", line)

	for _, args := range [][]string{
		{"api", "snapshot"},
		{"tab", "create", "--cwd", "/work/shop", "--label", "third", "--no-focus", "--env", "A=b"},
		{"tab", "rename", worker.Tab, "new name"},
		{"tab", "focus", worker.Tab},
		{"pane", "split", worker.ID, "--direction", "down", "--ratio", "0.75", "--no-focus"},
		{"pane", "swap", "--source-pane", lead.ID, "--target-pane", side.ID},
		{"pane", "resize", "--pane", side.ID, "--direction", "left", "--amount", "0.02"},
		{"agent", "prompt", worker.ID, "whaleshark: a message is waiting. Run: whaleshark mail"},
		{"pane", "report-agent", worker.ID, "--source", "test", "--agent", "claude", "--state", "blocked"},
		{"agent", "start", "second", "--kind", "claude", "--pane", side.ID, "--timeout", "180000", "--", "--model", "small"},
		{"pane", "release-agent", worker.ID, "--source", "test", "--agent", "claude"},
		{"notification", "show", "a title", "--body", "a body", "--sound", "request"},
		{"server", "reload-config"},
		{"pane", "close", side.ID},
		{"tab", "close", worker.Tab},
	} {
		answer := f.Handle(testkit.FakeCall{Args: args})
		if answer.Exit != 0 {
			t.Errorf("herdr %s: exit %d: %s", strings.Join(args, " "), answer.Exit, answer.Stderr)
		}
		check("herdr "+strings.Join(args, " "), "success_response", []byte(answer.Stdout))
	}
	answer := f.Handle(testkit.FakeCall{Args: []string{"pane", "close", "w1:p99"}})
	check("a refusal", "error_response", []byte(answer.Stderr))

	conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	seen := map[string]bool{}
	for {
		line, err := lines.ReadBytes('\n')
		if err != nil {
			break
		}
		var event struct{ Event string }
		json.Unmarshal(line, &event)
		seen[event.Event] = true
		name := "event"
		if event.Event == herdr.Status {
			name = "subscription_event"
		}
		check("the event line "+event.Event, name, line)
	}
	for _, kind := range []string{herdr.Created, herdr.Updated, herdr.Closed, herdr.Focused, herdr.Status, herdr.Released, herdr.TabClosed, herdr.TabNamed} {
		if !seen[kind] {
			t.Errorf("the fake pushed no %s line", kind)
		}
	}
}

// shape lists every path of a JSON value with the kind of value found there.
func shape(v any, path string, into map[string]string) {
	switch x := v.(type) {
	case map[string]any:
		for name, each := range x {
			shape(each, path+"."+name, into)
		}
	case []any:
		for _, each := range x {
			shape(each, path+"[]", into)
		}
	case nil:
	default:
		into[path] = fmt.Sprintf("%T", v)
	}
}

func TestReadOnlyCallsHaveTheShapesOfTheRealHerdr(t *testing.T) {
	f, _, _, _ := busy(t)
	for _, args := range [][]string{{"status", "--json"}, {"api", "snapshot"}} {
		var realValue, fakeValue any
		if err := json.Unmarshal(realHerdr(t, args...), &realValue); err != nil {
			t.Fatal(err)
		}
		json.Unmarshal([]byte(f.Handle(testkit.FakeCall{Args: args}).Stdout), &fakeValue)
		realShape, fakeShape := map[string]string{}, map[string]string{}
		shape(realValue, "", realShape)
		shape(fakeValue, "", fakeShape)
		for path, kind := range fakeShape {
			// A list that is empty in the real herdr at this moment shows no shape to compare with.
			list, shown := path[:strings.LastIndex(path, "[]")+1], false
			for p := range realShape {
				shown = shown || strings.HasPrefix(p, list)
			}
			if !shown {
				continue
			}
			if realShape[path] != kind && !(realShape[path] == "" && optional[path[strings.LastIndex(path, ".")+1:]]) {
				t.Errorf("herdr %s: %s is %s in the fake and %q in the real herdr", strings.Join(args, " "), path, kind, realShape[path])
			}
		}
	}
	if v := strings.TrimSpace(string(realHerdr(t, "--version"))); v != "herdr "+Version {
		t.Logf("the real herdr is %q; the fake was written against %s", v, Version)
	}

	// Both go through the same adapter and come out as the same kind of picture.
	real, err := herdr.New(kit()).Snapshot(context.Background())
	if err != nil {
		t.Skipf("no running herdr server to compare with: %v", err)
	}
	for _, s := range []*contract.Snapshot{real, {Panes: panes(t, f)}} {
		for _, p := range s.Panes {
			if p.ID == "" || p.Tab == "" || p.Workspace == "" || p.Terminal == "" || p.Status == "" || (p.Agent == "" && p.Name != "") {
				t.Errorf("a pane read through the adapter is not whole: %+v", p)
			}
		}
	}
}

// optional are the fields herdr leaves out when it has nothing to say, so
// the real herdr may show none of them at the moment of the test.
var optional = map[string]bool{"agent": true, "name": true, "source": true, "kind": true, "value": true,
	"focused_pane_id": true, "focused_tab_id": true, "focused_workspace_id": true, "active_tab_id": true}
