package contract

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

// Tool is the description of one agent tool: all the program knows of it.
// Two places hold descriptions and no third: the table compiled in below,
// and one file a tool in the login's own settings folder, tools/<name>.toml.
// A project's file, a team file, a brief and a task can name a tool and
// never describe one: Tools, the one reader, has no parameter for a project.
//
// Run is the program and its first arguments, a list and never a line for a
// shell. Model, Folder and Resume are the options that name a model, give
// one more folder and continue a conversation, with {model}, {dir} and
// {session} where the value goes; an empty one means the tool has no such
// option. Each says Folder's option stands before every folder. Hooks is
// whose form of hooks file the tool reads: HooksClaude, HooksCodex or
// HooksNone. Suits is the person's sentence for the orchestrator. Env is
// what the tool is started with besides, for a local model; a value of it
// can be a key, so no screen and no log shows one.
type Tool struct {
	Name   string   `toml:"-"`
	Label  string   `toml:"label"`
	Run    []string `toml:"run"`
	Model  []string `toml:"model,omitempty"`
	Folder []string `toml:"folder,omitempty"`
	Each   bool     `toml:"each,omitempty"`
	Resume []string `toml:"resume,omitempty"`
	Hooks  string   `toml:"hooks"`
	// Rules is the file the tool reads when it starts, named from the home
	// folder: where init writes the short block that tells an agent its part.
	Rules  string            `toml:"rules,omitempty"`
	Suits  string            `toml:"suits,omitempty"`
	Screen ToolScreen        `toml:"screen,omitempty"`
	Env    map[string]string `toml:"env,omitempty"`
	// Proven: it has run for real under this program. Mine: the description,
	// or a part of it, is the login's own file.
	Proven bool `toml:"-"`
	Mine   bool `toml:"-"`
	// Tried is what the one test of the tool (doctor --try) last saw on this
	// computer, and the day it ran: a local truth, shown and never typed,
	// and no rule reads it.
	Tried ToolTried `toml:"tried,omitempty"`
}

// ToolTried is one run of a tool's test: the day as 2026-10-12, and of the
// four things below the ones that were seen, in their order.
type ToolTried struct {
	Day string   `toml:"day,omitempty"`
	Saw []string `toml:"saw,omitempty"`
}

var TriedSaw = []string{"started", "took its task", "worked", "came to rest"}

// The three ranks of a tool, as a person reads them. A rank is worked out
// and never claimed: proven has run for real under this program, day after
// day; ready is a description that ships, written from its maker's manual;
// any other tool that runs in a terminal is started, given its task and read.
const (
	RankProven   = "proven"
	RankReady    = "ready, not proven"
	RankTerminal = "works as a terminal tool"
)

func (t Tool) Rank() string {
	switch {
	case t.Proven:
		return RankProven
	case slices.ContainsFunc(builtIn, func(b Tool) bool { return b.Name == t.Name }):
		return RankReady
	}
	return RankTerminal
}

// Facts is the rest of a tool's line in the one table that doctor, the
// settings screen and the lead agent's guide print: what is known of it,
// what its test saw here, and "experimental" for all but a proven one.
func (t Tool) Facts() string {
	facts := []string{"busy or quiet only", "not held by Stop all"}
	if t.Exact() {
		facts[0] = "exact states"
	}
	if t.Held() {
		facts[1] = "held by Stop all"
	}
	if t.Hooks == HooksClaude {
		facts = append(facts, "context bar")
	}
	if len(t.Resume) > 0 {
		facts = append(facts, "picks up after a restart")
	}
	if t.Tried.Day != "" {
		facts = append(facts, "tried here "+t.Tried.Day+": "+strings.Join(t.Tried.Saw, ", "))
	}
	if !t.Proven {
		facts = append(facts, "experimental")
	}
	return strings.Join(facts, " · ")
}

// ToolScreen is words on a tool's screen, each optional and as fragile as a
// screen is: the line under its prompt box while it waits and while it
// works, two phrases that together mean a question is open, and the phrase
// of its question about a folder, asked before any hook runs.
type ToolScreen struct {
	// Title is the terminal title the tool shows while its chat has no name.
	// A tool that names one sets the title to a chat's name once it has one,
	// and the main agent's tab can then follow a name the person gives.
	Title    string   `toml:"title,omitempty"`
	Ready    string   `toml:"ready,omitempty"`
	Working  string   `toml:"working,omitempty"`
	Question []string `toml:"question,omitempty"`
	Trust    string   `toml:"trust,omitempty"`
}

const (
	HooksClaude = "claude"
	HooksCodex  = "codex"
	HooksNone   = "none"
	// ToolsDir is the folder of the login's own descriptions, in its settings
	// folder; DefaultTool is the tool of an agent nobody named one for.
	ToolsDir    = "tools"
	DefaultTool = "claude"
)

// builtIn are the two descriptions compiled in. Claude Code's has run for
// real since phase 1. Codex's options and hooks are its maker's pages', read
// and not yet run, and of its screen no word is known. A built-in's program
// is found on the path, as it always was; a person's file names a full path.
var builtIn = []Tool{
	{Name: "claude", Label: "Claude Code", Run: []string{"claude"}, Model: []string{"--model", "{model}"},
		Folder: []string{"--add-dir", "{dir}"}, Resume: []string{"--resume", "{session}"}, Hooks: HooksClaude, Rules: ".claude/CLAUDE.md", Proven: true,
		Suits:  "the default; anything",
		Screen: ToolScreen{Title: "Claude Code", Ready: "? for shortcuts", Working: "esc to interrupt", Question: []string{"Do you want to", "Esc to cancel"}, Trust: "trust this folder"}},
	{Name: "codex", Label: "Codex", Run: []string{"codex"}, Model: []string{"--model", "{model}"},
		Folder: []string{"--add-dir", "{dir}"}, Each: true, Resume: []string{"resume", "{session}"}, Hooks: HooksCodex, Rules: ".codex/AGENTS.md"},
}

// BuiltIn is the descriptions compiled in, for whoever lists them apart.
func BuiltIn() []Tool { return slices.Clone(builtIn) }

var toolName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,15}$`)

// Tools returns every tool that is wired in, the built-in ones first, and
// one sentence for each thing it did not use: a folder or a file another
// login can write, a name outside the rule, a file that does not read, a
// program that is no full path or lies inside a project of the login's
// list. A file with a built-in's name takes its place field by field.
func Tools(p Platform, d Dirs) (tools []Tool, faults []string) {
	if tools = slices.Clone(builtIn); d.Config == "" {
		return tools, nil // no folder of the login's is known: never one beside where the command stands
	}
	dir := filepath.Join(d.Config, ToolsDir)
	files, _ := filepath.Glob(filepath.Join(dir, "*.toml"))
	if others, _ := p.WritableByOthers(dir); others && len(files) > 0 {
		return tools, []string{dir + " can be written by another login, so no tool described there is used"}
	}
	roots, _ := ReadProjects(p.Peek, d.State)
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".toml")
		at := slices.IndexFunc(tools, func(t Tool) bool { return t.Name == name })
		t := Tool{Name: name, Hooks: HooksNone}
		if at >= 0 {
			t = tools[at]
		}
		data, err := p.Peek(file)
		if others, _ := p.WritableByOthers(file); others {
			err = errors.New("another login can write it")
		}
		if err == nil {
			err = toml.Unmarshal(data, &t)
		}
		if t.Mine, t.Proven = true, t.Proven && slices.Equal(t.Run, builtIn[max(at, 0)].Run); err == nil {
			err = t.Check(p, roots)
		}
		switch {
		case err != nil:
			faults = append(faults, fmt.Sprintf("%s is not used: %v", file, err))
		case at >= 0:
			tools[at] = t
		default:
			tools = append(tools, t)
		}
	}
	return tools, faults
}

// Found says whether the tool's program is there to start, which with its
// description is what "wired in" means: a list of tools to choose from
// holds the found ones only.
func (t Tool) Found() bool {
	_, err := exec.LookPath(t.Run[0])
	return err == nil
}

// FindTool is the wired tool of a name, the default one for no name.
func FindTool(tools []Tool, name string) (Tool, bool) {
	if name == "" {
		name = DefaultTool
	}
	at := slices.IndexFunc(tools, func(t Tool) bool { return t.Name == name })
	if at < 0 {
		return Tool{}, false
	}
	return tools[at], true
}

// Check refuses a description that must not be stored or used: a name
// outside the rule, no program, a program of the login's own file that is no
// full path or lies inside one of the login's projects (roots), a form of
// hooks nobody knows, and any word that could be read as two.
func (t Tool) Check(p Platform, roots []string) error {
	switch {
	case !toolName.MatchString(t.Name):
		return fmt.Errorf("a tool's name is a small letter and up to fifteen more, digits or dashes: %.20q", t.Name)
	case len(t.Run) == 0 || t.Run[0] == "":
		return errors.New("it names no program to run")
	case t.Hooks != HooksClaude && t.Hooks != HooksCodex && t.Hooks != HooksNone:
		return fmt.Errorf("hooks is %s, %s or %s", HooksClaude, HooksCodex, HooksNone)
	case t.Rules != "" && !filepath.IsLocal(filepath.FromSlash(t.Rules)):
		return fmt.Errorf("the file it reads at its start is named from your home folder and stays inside it: %.40q does not", t.Rules)
	}
	if own := !slices.ContainsFunc(builtIn, func(b Tool) bool { return slices.Equal(b.Run, t.Run) }); own {
		if !filepath.IsAbs(t.Run[0]) {
			return fmt.Errorf("its program must be a full path, not %q", t.Run[0])
		}
		for _, root := range roots {
			if rel, err := filepath.Rel(p.PathKey(root), p.PathKey(t.Run[0])); err == nil && filepath.IsLocal(rel) {
				return fmt.Errorf("its program lies inside the project %s", root)
			}
		}
	}
	for _, word := range slices.Concat(t.Run, t.Model, t.Folder, t.Resume, t.Tried.Saw, []string{t.Label, t.Suits, t.Rules, t.Tried.Day}) {
		if strings.ContainsFunc(word, steers) {
			return errors.New("a word of it holds a line break or a control character")
		}
	}
	return nil
}

// Fill is one option of a description with its value put in: Fill(t.Model,
// "model", "opus") is the arguments that name that model. An option the tool
// does not have gives none.
func Fill(option []string, name, value string) []string {
	out := make([]string, len(option))
	for i, word := range option {
		out[i] = strings.ReplaceAll(word, "{"+name+"}", value)
	}
	return out
}

// Folders is the arguments that give a tool more folders: its option before
// each, or once before them all.
func (t Tool) Folders(dirs ...string) (args []string) {
	at := slices.Index(t.Folder, "{dir}")
	if at < 0 || len(dirs) == 0 {
		return nil
	}
	for i, dir := range dirs {
		if i == 0 || t.Each {
			args = append(args, t.Folder[:at]...)
		}
		args = append(args, dir)
	}
	return args
}

// Exact reports whether the tool's own harness tells its states, so that
// working, at a prompt and idle are exact; without, its screen's words are
// read, and without those it is busy or quiet and no more. Held reports
// whether Stop all holds it at its next step, which only a gate does that
// was tried on the real tool.
func (t Tool) Exact() bool { return t.Hooks == HooksClaude || t.Hooks == HooksCodex }
func (t Tool) Held() bool  { return Gates[t.Hooks].Holds }

// WriteTool stores a description as the login's own file, private to it,
// after Check; RemoveTool takes the file away, and a built-in is then as it
// was compiled.
func WriteTool(p Platform, d Dirs, t Tool) error {
	roots, _ := ReadProjects(p.Peek, d.State)
	if err := t.Check(p, roots); err != nil {
		return err
	}
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(t); err != nil {
		return err
	}
	return replaceFile(p, filepath.Join(d.Config, ToolsDir, t.Name+".toml"), b.Bytes())
}

func RemoveTool(d Dirs, name string) error {
	if !toolName.MatchString(name) {
		return fmt.Errorf("no tool is called %.20q", name)
	}
	err := os.Remove(filepath.Join(d.Config, ToolsDir, name+".toml"))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no tool of your own is called %s", name)
	}
	return err
}

// replaceFile puts a finished file, private to the login, in the place of
// path, making its folder private too.
func replaceFile(p Files, path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return p.Replace(tmp.Name(), path)
}

// NotWired is what a start and "Try it" say of a tool that cannot be started.
func NotWired(name string) string {
	return fmt.Sprintf("No agent tool %.20q is wired in here: its description or its program is missing.", name)
}

// MainTab is what the main agent's tab is called until a person names it or
// its chat: the first tab of a window, and the tab a run's lead agent sits in.
const MainTab = "Orchestrator"

// TitleText is a terminal title without the one mark a tool turns in front
// of it while it works.
func TitleText(title string) string {
	words := strings.Fields(title)
	if len(words) > 1 && utf8.RuneCountInString(words[0]) == 1 && !unicode.IsLetter([]rune(words[0])[0]) && !unicode.IsDigit([]rune(words[0])[0]) {
		words = words[1:]
	}
	return strings.Join(words, " ")
}

// ChatName is the name of a chat as a tab's label, from the text of the
// title its tool set, or nothing: for a tool that names no title of its
// own, for that title itself, and for a text that could be read as an
// option or a path. A title is a stranger's text: it is cut as a task's
// name is, to 24 characters.
func ChatName(t Tool, text string) string {
	if t.Screen.Title == "" || strings.EqualFold(text, t.Screen.Title) || strings.HasPrefix(text, "-") || strings.ContainsAny(text, `/\~`) {
		return ""
	}
	if r := []rune(text); len(r) > 24 {
		text = strings.TrimSpace(string(r[:24]))
	}
	return text
}
