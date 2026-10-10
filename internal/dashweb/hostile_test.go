package dashweb

import (
	"bytes"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// hostile is what an agent, a task's name, a file's name, a branch or a
// question might hold to become markup, a script, a link or a style.
const hostile = `"'><script>alert(1)</script><img src=x onerror=alert(1)></pre></title><style>*{display:none}</style> javascript:alert(1) //elsewhere.example`

// spoil puts the hostile text into every word of a value, however deep, one
// entry into every list and every map, and a moment into every time.
func spoil(v reflect.Value, depth int) {
	switch v.Kind() {
	case reflect.String:
		v.SetString(hostile)
	case reflect.Pointer:
		if depth < 6 {
			v.Set(reflect.New(v.Type().Elem()))
			spoil(v.Elem(), depth+1)
		}
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[time.Time]() {
			v.Set(reflect.ValueOf(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)))
			return
		}
		for i := range v.NumField() {
			if v.Field(i).CanSet() {
				spoil(v.Field(i), depth+1)
			}
		}
	case reflect.Slice:
		if depth < 6 {
			one := reflect.New(v.Type().Elem()).Elem()
			spoil(one, depth+1)
			v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), one))
		}
	case reflect.Map:
		key, value := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		spoil(key, depth+1)
		spoil(value, depth+1)
		v.Set(reflect.MakeMap(v.Type()))
		v.SetMapIndex(key, value)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int64:
		v.SetInt(7)
	}
}

// Whatever words the view holds, every screen stays the page's own: no tag,
// no script, no style and no handler of the text's making, no attribute it
// broke out of, and no address that leads anywhere but the page itself or a
// running site on this computer.
func TestHostileTextInEveryWordOfEveryScreen(t *testing.T) {
	tag := regexp.MustCompile(`<[a-zA-Z/!][^>]*>`)
	address := regexp.MustCompile(`\s(?:href|src|action)="([^"]*)"`)
	own := regexp.MustCompile(`^<(?:/?(?:html|head|body|main|header|footer|nav|section|article|details|summary|form|table|tr|th|td|dl|dt|dd|h1|h2|p|pre|span|div|a|b|button|progress)|!doctype|meta|title|/title|link|script|/script|input|img)\b`)
	for _, screen := range []string{contract.ScreenTeam, contract.ScreenPlan, contract.ScreenMessages, contract.ScreenTask, contract.ScreenJobs, contract.ScreenLead, contract.ScreenPair, contract.ScreenRefused} {
		for _, phone := range []bool{false, true} {
			p := new(contract.Page)
			spoil(reflect.ValueOf(p).Elem(), 0)
			p.Screen, p.Phone = screen, phone
			var out bytes.Buffer
			if err := Page(&out, p); err != nil {
				t.Fatalf("%s: %v", screen, err)
			}
			text := out.String()
			if !strings.Contains(text, "alert(1)") && screen != contract.ScreenPair {
				t.Errorf("%s shows none of the hostile text: the test proves nothing", screen)
			}
			for _, bad := range []string{"<script>alert", "<img src=x", "<style>", "</pre></title>", `"'>`} {
				if strings.Contains(text, bad) {
					t.Errorf("%s (phone %v) carries %q as written", screen, phone, bad)
				}
			}
			for _, m := range tag.FindAllString(text, -1) {
				// What stands between an attribute's quotes is its value and no more.
				bare := regexp.MustCompile(`"[^"]*"`).ReplaceAllString(m, `""`)
				if !own.MatchString(strings.ToLower(m)) || regexp.MustCompile(`(?i)\son[a-z]+\s*=|\sstyle\s*=`).MatchString(bare) {
					t.Errorf("%s (phone %v) has a tag that is not the page's own: %.120s", screen, phone, m)
				}
				if strings.HasPrefix(m, "<script") && !regexp.MustCompile(`^<script src="/a/[a-z.]+" defer>$`).MatchString(m) {
					t.Errorf("%s has a script of another making: %s", screen, m)
				}
			}
			for _, m := range address.FindAllStringSubmatch(text, -1) {
				if to := m[1]; !strings.HasPrefix(to, "/") || strings.HasPrefix(to, "//") {
					if !regexp.MustCompile(`^http://localhost:\d+/$`).MatchString(to) {
						t.Errorf("%s (phone %v) has an address that leaves the page: %q", screen, phone, to)
					}
				}
			}
		}
	}
}
