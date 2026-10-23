package inbox

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// resetLine is how an agent tool says when its usage limit resets: "resets
// 3pm", "will reset at 5:30pm (Europe/London)", "resets Oct 27, 9am".
var resetLine = regexp.MustCompile(`(?i)resets?\s+(?:at\s+)?(?:([a-z]{3})[a-z]*\s+(\d{1,2}),?\s+(?:at\s+)?)?(\d{1,2})(?::(\d{2}))?\s*(am|pm)(?:\s*\(([^)\s]+)\))?`)

// limitLine is a screen that says its agent tool met a usage limit: without
// one, workers that stopped together are only the person's to look at.
var limitLine = regexp.MustCompile(`(?i)\b(usage|rate|hour|weekly|session)[ -]limit\b|\blimit (reached|will reset|resets)|hit your limit`)

// resets is the next moment after now that the last such line on a screen
// names, zero where none can be read.
func resets(screen string, now time.Time) time.Time {
	all := resetLine.FindAllStringSubmatch(screen, -1)
	if len(all) == 0 {
		return time.Time{}
	}
	m := all[len(all)-1]
	where := now.Location()
	if zone, err := time.LoadLocation(m[6]); err == nil && m[6] != "" {
		where = zone
	}
	hour, _ := strconv.Atoi(m[3])
	minute, _ := strconv.Atoi(m[4])
	y, month, day := now.In(where).Date()
	next := []int{0, 0, 1} // a time alone is the next such time; with a date, that date's next
	if m[1] != "" {
		named, err := time.Parse("Jan", strings.ToUpper(m[1][:1])+strings.ToLower(m[1][1:]))
		if err != nil {
			return time.Time{}
		}
		month, next = named.Month(), []int{1, 0, 0}
		day, _ = strconv.Atoi(m[2])
	}
	if hour > 12 || minute > 59 {
		return time.Time{}
	}
	at := time.Date(y, month, day, hour%12+map[bool]int{true: 12}[strings.EqualFold(m[5], "pm")], minute, 0, 0, where)
	for i := 0; !at.After(now) && i < 2; i++ {
		at = at.AddDate(next[0], next[1], next[2])
	}
	return at
}

// limit is the sweep's row for a usage limit. Most workers stopped together
// and a screen says a limit was met: the run is paused until the reset their
// tool printed, its hold file
// holding every gated agent (Kit.Hold), and the person is told once, by the
// item that said so. At that moment it is resumed and the agents are woken
// as at a hold's end; with no moment read it waits for whaleshark resume.
func (sw sweeper) limit(root, run string, now time.Time) {
	k := sw.k
	s, err := k.Store.Read(root, run)
	if err != nil || s.Run.Parent != nil {
		return
	}
	if p := s.Run.Paused; p != nil {
		if p.By == contract.LimitBy && !p.Until.IsZero() && !now.Before(p.Until) {
			k.Store.Change(root, run, func(s *contract.State) error {
				if p := s.Run.Paused; p != nil && p.By == contract.LimitBy {
					return k.Rules.Resume(s, now)
				}
				return nil
			})
		}
		return
	}
	var item *contract.Question
	for _, q := range s.Questions {
		if q.From == contract.FromTool && q.Cause == contract.CauseTogether && q.State != contract.QuestionClosed && s.Run.Limited.Before(q.CreatedAt) {
			item = q
		}
	}
	if item == nil {
		return
	}
	var until time.Time
	met := false
	for _, a := range s.Attempts {
		if !a.State.Live() || a.Place.Pane == "" || !until.IsZero() {
			continue
		}
		if text, err := k.Terms.Screen(a.Place.Pane); err == nil && limitLine.MatchString(text) {
			met, until = true, resets(text, now)
		}
	}
	if !met {
		return
	}
	k.Store.Change(root, run, func(s *contract.State) error {
		q := s.Questions[item.ID]
		if s.Run.Paused != nil || q == nil || q.State == contract.QuestionClosed {
			return nil
		}
		when := "resume it with whaleshark resume once your limit has reset"
		if !until.IsZero() {
			when = "it resumes by itself at " + until.Local().Format("15:04 Mon")
		}
		s.Run.Paused, s.Run.Limited = &contract.Pause{At: now, By: contract.LimitBy, Until: until}, q.CreatedAt
		q.Text = "Most workers stopped together at the usage limit. Nothing new starts; " + when + "."
		k.Rules.Raise(s, contract.Event{Kind: "paused", Text: "the usage limit: " + when}, now)
		return nil
	})
}
