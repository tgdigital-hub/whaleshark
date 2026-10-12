package rules

import (
	"fmt"
	"strings"
	"time"

	"github.com/tgdigital-hub/whaleshark/internal/contract"
)

// signs names the signs that hold for an attempt at work, and says whether
// they are the verdict: a hard one, two at once, or a soft one for twice
// its span. A note is said again and again when it stands three times.
func signs(t *contract.Task, a *contract.Attempt, exact bool, limits contract.ProjectFile, now time.Time) (why string, stuck bool) {
	h, l, same := a.Seen, limits.Limits, 0
	if a.Progress != nil {
		same = a.Progress.Same + 1
	}
	quiet, rest := time.Duration(l.QuietSeconds)*time.Second+contract.QuietMore, now.Sub(h.LastWorking)
	holds := map[string][2]bool{ // whether it holds, and whether for twice its span
		"stale":    {exact && a.Leads == "" && h.StaleFlagged, now.Sub(lastNote(a)) >= contract.StuckTwice*time.Duration(l.StaleMinutes)*time.Minute},
		"quiet":    {exact && h.QuietFlagged && rest >= quiet, rest >= contract.StuckTwice*quiet},
		"overtime": {h.OvertimeFlagged, now.Sub(a.StartedAt) >= contract.StuckTwice*time.Duration(l.MaxMinutes)*time.Minute},
		"same":     {same >= contract.StuckTimes, same >= contract.StuckTwice*contract.StuckTimes},
		"forgot":   {exact && h.Emptied >= contract.StuckTwice, h.Emptied >= contract.StuckTwice*contract.StuckTwice},
		"check":    {t.CheckFails >= contract.StuckTimes},
		"back":     {a.Round >= contract.StuckTimes || t.Synced >= contract.StuckTimes},
	}
	var names []string
	for _, sign := range contract.Signs {
		if on := holds[sign.Name]; on[0] {
			names, stuck = append(names, sign.Name), stuck || sign.Hard || on[1]
		}
	}
	return strings.Join(names, ","), stuck || len(names) >= contract.StuckSoft
}

func stuckText(who string, a *contract.Attempt, now time.Time) string {
	return fmt.Sprintf("%s looks stuck for %dm: %s.", who, int(now.Sub(a.Seen.StuckSince).Minutes()), contract.StuckWhy(a.Seen.StuckWhy))
}

// Going never judges an agent at a prompt or one the terminals have lost,
// and ends the episode of one that asked or reported. The last message to
// the attempt is when somebody last dealt with it: tell, a result sent back
// and a sync each leave one. The context figure is kept when it moved far.
func (r Rules) Going(s *contract.State, id string, exact bool, ctx contract.CtxFile, limits contract.ProjectFile, climb time.Duration, now time.Time) (changed, up bool) {
	a := s.Attempts[id]
	if a == nil || s.Run.Paused != nil || open(s) != nil {
		return false, false
	}
	t, h, why, stuck := s.Tasks[a.Task], &a.Seen, "", false
	if d := h.Ctx - ctx.Pct; ctx.Known && exact && (d >= contract.CtxDrop || -d >= contract.CtxDrop/3) {
		if h.Ctx, changed = ctx.Pct, true; d > 0 {
			h.Emptied++
		}
	}
	switch {
	case a.State != contract.AttemptWorking:
	case h.Status == contract.StatusBlocked || !h.GoneSince.IsZero():
		return changed, false
	default:
		why, stuck = signs(t, a, exact, limits, now)
	}
	told, low := h.StuckAt, s.Run.Parent != nil
	if n := len(a.Mail); n > 0 && a.Mail[n-1].At.After(told) {
		told = a.Mail[n-1].At
	}
	switch {
	case !stuck && h.StuckSince.IsZero(), stuck && !h.StuckSince.IsZero() && (h.StuckTold >= contract.ToldPerson || now.Sub(told) < climb):
		return changed, false
	case !stuck:
		up = low && h.StuckTold > contract.ToldLeader
		h.StuckSince, h.StuckWhy, h.StuckTold, h.StuckAt = time.Time{}, "", 0, time.Time{}
		withdraw(s, contract.CauseStuck, a.ID)
	case h.StuckSince.IsZero():
		h.StuckSince, h.StuckWhy, h.StuckTold, h.StuckAt = now, why, contract.ToldLeader, now
		raise(s, "stuck", t, a, t.Name+" looks stuck: "+contract.StuckWhy(why)+". Look: whaleshark show "+t.ID+" --screen. Then tell it, stop it, or start it again.",
			map[string]any{"why": why, "level": contract.ToldLeader}, now)
	case low:
		h.StuckWhy, h.StuckTold, h.StuckAt, up = why, h.StuckTold+1, now, true
	default:
		h.StuckWhy, h.StuckTold, h.StuckAt = why, contract.ToldPerson, now
		r.Need(s, contract.Question{Form: contract.FormTodo, From: contract.FromTool, Cause: contract.CauseStuck, Task: t.ID, Attempt: a.ID,
			Text: stuckText(t.Name, a, now) + " The lead agent was told."}, now)
	}
	return true, up
}

// Passed knows its to-do again by the group and the attempt's id below.
func (r Rules) Passed(up, low *contract.State, id string, now time.Time) error {
	a, t, err := attempt(low, id)
	name, g := group(up, low)
	switch {
	case err != nil:
		return err
	case g == nil || open(up) != nil:
		return notFleet(up, low)
	}
	var q *contract.Question
	for _, x := range up.Questions {
		if x.Cause == contract.CauseStuck && x.Task == g.ID && x.Attempt == a.ID && x.State != contract.QuestionClosed {
			q = x
		}
	}
	text := stuckText(name+" › "+t.Name, a, now)
	switch told := a.Seen.StuckTold; {
	case told < contract.ToldOrchestrator && q != nil:
		q.State = contract.QuestionClosed
	case told == contract.ToldOrchestrator:
		raise(up, "stuck", g, nil, text+" Its leader was told.", map[string]any{"why": a.Seen.StuckWhy, "level": told, "fleet": name, "task": t.ID}, now)
	case told == contract.ToldPerson && q == nil:
		r.Need(up, contract.Question{Form: contract.FormTodo, From: contract.FromTool, Cause: contract.CauseStuck, Task: g.ID, Attempt: a.ID,
			Text: text + " Its leader and the orchestrator were told."}, now)
	}
	return nil
}
