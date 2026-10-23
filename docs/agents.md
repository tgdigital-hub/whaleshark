# Agent tools

WhaleShark runs any coding agent that works in a terminal. It starts the agent in a tab, gives it
its task as one line ("Read and follow …/prompt.md"), and then reads the agent's screen to see
whether it is busy. How much more it can do depends on the tool:

- **Hooks.** Claude Code and Codex can run a command at each step they take. WhaleShark uses that
  to know exactly whether an agent is working, waiting at a question or idle, and to hold it at
  its next step when you press Stop all. Every other tool is seen as *busy* or *quiet*, and Stop
  all reaches it only at its next `whaleshark` command.
- **Screen words.** A tool's description can name the words its screen shows while it waits,
  while it works and while it asks a question. With these words WhaleShark tells those states
  apart even without hooks. Only Claude Code's words are known. No maker documents theirs, so for
  every other tool they are left empty until someone has watched the real tool.
- **Options.** WhaleShark uses a tool's option for a model (`--model`), for one more folder (a
  worker's prompt and brief lie outside its copy of the code), for continuing a conversation
  after a restart, for a worker's effort (`task add --effort`, `[agent] effort`) and for a
  settings file every worker is given (`[agent] settings`). Only Claude Code has the last two
  (`--effort`, `--settings`). A tool without one of these simply goes without.
- **A rules file.** `whaleshark init` writes a short block into the file a tool reads at its start,
  so that your main agent knows its part. A tool with no such file in your home folder gets
  nothing written. Then begin your first message to it with:
  ``Run `whaleshark guide lead` and follow it.``

## The tools

"Built in" means its description ships with WhaleShark: you only install the tool. **Only Claude
Code has been run under WhaleShark.** Every other built-in was written from its maker's pages
(listed under [Sources](#sources)) and has never been run here. It is shown as *ready, not
proven* and is experimental.

| Tool | Models | Started as | In WhaleShark | Hooks | Model option | More folders | Rules file it reads (under your home folder) | What still falls short |
|---|---|---|---|---|---|---|---|---|
| Claude Code | cloud (or an Anthropic-compatible endpoint) | `claude` | built in, **proven** | yes | `--model` | `--add-dir` | `.claude/CLAUDE.md` | nothing known |
| OpenAI Codex CLI | cloud, local (`--oss`) | `codex` | built in, not proven | yes, not tried | `--model` | `--add-dir` | `.codex/AGENTS.md` (or `$CODEX_HOME/AGENTS.md`) | screen words unknown; not held by Stop all; hooks of a project only once you trust it |
| Google Gemini CLI | cloud | `gemini` | built in, not proven | none WhaleShark reads | `--model` | `--include-directories` | `.gemini/GEMINI.md` | busy or quiet only; not held; no continuing after a restart; see the note below |
| Qwen Code | cloud, local (OpenAI-compatible) | `qwen` | built in, not proven | none WhaleShark reads | `--model` | `--include-directories` | `.qwen/QWEN.md` | busy or quiet only; not held; no continuing after a restart |
| OpenCode | cloud, local (OpenAI-compatible) | `opencode` | built in, not proven | none WhaleShark reads | `--model provider/model` | none | `.config/opencode/AGENTS.md` | as above, and it asks before it reads the prompt outside its folder |
| Cursor CLI | cloud | `agent` | built in, not proven | none WhaleShark reads | `--model` | `--add-dir` | none | as above, and no rules file: tell your main agent its part |
| Hermes Agent (Nous Research) | cloud, local (OpenAI-compatible) | `hermes` | built in, not proven | none WhaleShark reads | `--model` | none | none | as above, and no rules file and no folder option |
| Goose | cloud, local (Ollama, OpenAI-compatible) | `goose session` | built in, not proven | none WhaleShark reads | none (`GOOSE_MODEL`) | none | `.config/goose/.goosehints` | as above; a model named on `start` is not passed |
| Crush (Charm) | cloud, local (Ollama, LM Studio, llama.cpp, OpenAI-compatible) | `crush` | built in, not proven | none WhaleShark reads | none in its chat | none | `.config/crush/CRUSH.md` | as above; a model named on `start` is not passed |
| Amp | cloud | `amp` | built in, not proven | none WhaleShark reads | none (a mode instead) | none | `.config/amp/AGENTS.md` | as above, and Amp asks no approval before it runs a tool |
| Aider | cloud, local (Ollama, OpenAI-compatible) | `aider` | example file below | none | `--model` | none | none | it reads no rules file by itself, and asks before it runs a command, so it cannot follow a worker's prompt alone |
| Grok Build (xAI) | cloud, local (OpenAI-compatible) | `grok` | example file below | none WhaleShark reads | `--model` | none known | a folder, `.grok/rules/` | the facts of its rules folder were not sure enough to build it in |

In "What still falls short", *busy or quiet only* means the screen's words are unknown. *Not held*
means Stop all reaches the agent only at its next `whaleshark` command.

Notes:

- **Gemini CLI.** Google's notice of June 2026 says Gemini CLI no longer serves Google AI Pro,
  Ultra and free Code Assist users. It still works with paid Gemini API keys. Its successor,
  Antigravity CLI, has not been looked at here.
- **Cursor CLI** is started as `agent`. Any other program of that name on your path would be
  taken for it.
- **Grok.** xAI's own CLI is Grok Build. A community tool, `superagent-ai/grok-cli`, is also
  started as `grok` and is not xAI's.
- **The first start in a folder.** Many tools ask, the first time, whether you trust the folder.
  WhaleShark knows only Claude Code's question. With any other tool, start it once yourself in
  the project's folder and answer before WhaleShark starts it there. Otherwise WhaleShark may type
  the agent's first line into that question.
- **The model option.** For a tool whose model is chosen by a variable (Goose) or in its own
  settings (Crush, Amp, OpenCode's providers), set it there. `--model` on `whaleshark start` is
  passed only to a tool that has a model option.

What can only be proven by installing and running each tool, and is open for every tool but
Claude Code:

1. that it starts from its description, takes its first line pasted into it, works and comes to rest;
2. what its screen shows while ready, while working, and while it asks a question, and while it asks about a new folder (these become its screen words);
3. that it reads the rules block `whaleshark init` writes, and follows a worker's prompt to the end, including `whaleshark report`;
4. for Codex, that its hooks fire and report a session, so that Stop all, exact states and continuing after a restart work.

`[ Try it ]` in Settings › Agent tools (or `whaleshark doctor --try NAME`) runs the first of these
on your machine and shows what it saw.

## Setting a tool up

1. Install the tool and sign in to it, as its maker's page says.
2. Start it once yourself in your project's folder, answer its first questions, and quit.
3. In the project, run `whaleshark init` and name the tool when asked, or run `whaleshark init --agent gemini`.
   This writes the rules block into the tool's rules file, if the tool has one.
4. Start it in the first tab of the window. Workers get it with `--agent NAME` on `whaleshark task add` or
   `whaleshark start`, or by Settings › Fleet Mode for leaders and their workers.

`whaleshark doctor` lists every tool, whether its program is found, and what is known of it.

## Local and remote models

Every tool reaches its model over HTTP. A model on your own machine (Ollama, LM Studio, llama.cpp's
`llama-server`, vLLM) is one more address for the tool. WhaleShark needs nothing for it: it starts
the tool, and the tool talks to the model.

The servers' addresses, from their own pages:

| Server | OpenAI-compatible address |
|---|---|
| Ollama | `http://localhost:11434/v1` |
| LM Studio | `http://localhost:1234/v1` |
| llama.cpp `llama-server` | `http://localhost:PORT/v1`; start it with `--port 8080` so that the port is the one you think |
| vLLM | `http://localhost:8000/v1` |

### Giving the tool its settings

A tool reads its local model from its own settings file or from environment variables. A variable
you want only that tool to have goes into its description, private to your login. No screen and no
log shows its value:

```
whaleshark set tools.qwen.env.OPENAI_BASE_URL http://localhost:11434/v1
whaleshark set tools.qwen.env.OPENAI_API_KEY ollama
whaleshark set tools.qwen.env.OPENAI_MODEL qwen3-coder
```

An agent that WhaleShark starts (a worker, a fleet leader) is started with the engine's own
environment, then its tab's variables, then the tool's variables over those. The tool's variables
are read again at each start. A tool you start yourself by typing its name in a tab's shell, as you
do for your main agent, is a program of that shell: it gets the shell's environment and not the
tool's variables. For it, export the same variables in your shell's start-up file, or keep them in
the tool's own settings file, which works for both.

The engine's environment is the one it had when it started. A variable you export in a shell after
that reaches no agent until the engine is started again (`whaleshark engine stop`, then `whaleshark open`).

### Each tool

- **Codex**: `codex --oss` uses a local provider. `--local-provider ollama` (or `lmstudio`) picks
  which one, as does `oss_provider = "ollama"` in `~/.codex/config.toml`. Any other server is a
  `[model_providers.<id>]` table there, with `base_url`, and `model_provider = "<id>"`.
- **Qwen Code**: `OPENAI_BASE_URL`, `OPENAI_API_KEY` and `OPENAI_MODEL`, as above. Alternatively,
  `modelProviders` in its `settings.json`, with `security.auth.selectedType` set to `"openai"`.
- **OpenCode**: a provider in `opencode.json` with `"npm": "@ai-sdk/openai-compatible"`,
  `"options": { "baseURL": "http://localhost:11434/v1" }` and its models. Then
  `--model <provider>/<model>`.
- **Hermes Agent**: in `~/.hermes/config.yaml`, `model:` with `provider: custom`,
  `base_url: http://localhost:11434/v1` and `default: <model>`.
- **Goose**: `GOOSE_PROVIDER=ollama`, `GOOSE_MODEL=<model>` and `OLLAMA_HOST` (default
  `localhost:11434`), as the tool's variables. For another OpenAI-compatible server:
  `OPENAI_HOST`, `OPENAI_BASE_PATH` and `OPENAI_API_KEY`.
- **Crush**: its README adds a provider in its settings file (`~/.config/crush/crushrc`) with
  `provider add ollama --type ollama --base-url "http://localhost:11434/v1/"`. Types `lmstudio`,
  `llamacpp` and `openai-compat` (with `--base-url` and `--api-key`) exist too.
- **Aider**: `OLLAMA_API_BASE=http://localhost:11434` and `--model ollama_chat/<model>`. For any
  OpenAI-compatible server: `OPENAI_API_BASE` and `OPENAI_API_KEY` with `--model openai/<model>`.
- **Grok Build**: a `[model.<id>]` table in `~/.grok/config.toml` with `model` and
  `base_url = "http://localhost:11434/v1"`.
- **Claude Code**: `ANTHROPIC_BASE_URL` points it at an endpoint that speaks Anthropic's API.
- **Gemini CLI, Cursor CLI, Amp**: their pages describe no local OpenAI-compatible endpoint. Amp
  routes to custom endpoints that must be reachable from Amp, which a server on your own machine
  likely is not (not tried).

### A model on another machine

Either way, the tool on your machine talks to the server on the other one:

- **Through SSH** (encrypted; nothing on the other machine is opened to the network). Keep this
  running in a terminal of its own:

  ```
  ssh -N -L 11434:localhost:11434 you@gpu-box
  ```

  The other machine's Ollama is then `http://localhost:11434` here, and every setting above stays
  as written. For LM Studio, vLLM or llama-server, forward their port the same way.
- **Over the network.** Ollama listens on this machine only, unless `OLLAMA_HOST` is set to every
  address where it runs (Ollama's FAQ shows how). Then use `http://gpu-box:11434/v1` in the settings above. Ollama's port has no
  password and no encryption: do this only on a network you trust.

To have the agents themselves run on another machine, put WhaleShark there: see
[server.md](server.md).

## A tool of your own

A tool WhaleShark does not ship is described by one file, `tools/<name>.toml`, in your settings
folder:

| System | Folder |
|---|---|
| macOS, Linux | `~/.config/whaleshark/tools/` (or `$XDG_CONFIG_HOME/whaleshark/tools/`) |
| Windows | `%APPDATA%\whaleshark\tools\` |

A file with a built-in's name (`gemini.toml`) changes that description field by field, and leaves
the rest as it ships. The folder and its files must be writable by you alone. The program is a full
path (find it with `command -v aider`, or `where aider` on Windows) and may not lie inside one of
your projects. Settings › Agent tools writes the same file, as does `whaleshark set tools.NAME.FIELD VALUE`
(`whaleshark help set` lists the fields). Each field:

```toml
label = "Aider"                       # its name in words
run = ["/usr/local/bin/aider"]        # the program and its first arguments, a list
model = ["--model", "{model}"]        # how a model is named; leave out if it has none
folder = ["--add-dir", "{dir}"]       # how one more folder is given; leave out if none
each = true                           # that option stands before every folder
resume = ["--resume", "{session}"]    # only for a tool with hooks: the session comes from them
effort = ["--effort", "{effort}"]     # how its effort is set: low, medium, high or max
settings = ["--settings", "{file}"]   # how a settings file is given; leave out if none
hooks = "none"                        # "claude", "codex" or "none"
rules = ".aider/RULES.md"             # the file it reads at its start, under your home folder
suits = "small fixes"                 # a sentence for your orchestrator

[screen]                              # the words its screen shows; each is optional
ready = "…"
working = "…"
question = ["…", "…"]                 # all of these together mean a question is open
trust = "…"                           # its question about a new folder

[env]                                 # variables it is started with
OLLAMA_API_BASE = "http://localhost:11434"
```

Leave a screen word out rather than guess one: a wrong word makes WhaleShark believe the agent is
at rest when it is not. `[ Try it ]` shows the last line the screen had at rest.

### Ready-made files

Copy one into the folder above, put in your program's full path, and run `whaleshark doctor`.

**Aider** with a local Ollama, `tools/aider.toml`. Aider asks before it runs a command and reads no
rules file by itself, so it suits a task you watch, not a worker left alone:

```toml
label = "Aider"
run = ["/usr/local/bin/aider", "--model", "ollama_chat/qwen2.5-coder:32b"]
hooks = "none"
suits = "small edits you watch; it asks before every command"

[env]
OLLAMA_API_BASE = "http://localhost:11434"
```

**Grok Build**, `tools/grok.toml`. Its rules are files in the folder `~/.grok/rules/`, which
`whaleshark init` does not write into, so begin your main agent's first message with
``Run `whaleshark guide lead` and follow it.``:

```toml
label = "Grok Build"
run = ["/usr/local/bin/grok"]
model = ["--model", "{model}"]
hooks = "none"
```

**Codex on a local model**, `tools/codex-local.toml`, beside the built-in Codex:

```toml
label = "Codex, local model"
run = ["/usr/local/bin/codex", "--oss", "--local-provider", "ollama"]
model = ["--model", "{model}"]
folder = ["--add-dir", "{dir}"]
each = true
hooks = "codex"
```

**Qwen Code on a model on another machine** (through the SSH line above), `tools/qwen.toml`. It
changes the built-in's variables only:

```toml
[env]
OPENAI_BASE_URL = "http://localhost:11434/v1"
OPENAI_API_KEY = "ollama"
OPENAI_MODEL = "qwen3-coder"
```

**Goose with Ollama**, `tools/goose.toml`. Again only the variables:

```toml
[env]
GOOSE_PROVIDER = "ollama"
GOOSE_MODEL = "qwen3-coder"
OLLAMA_HOST = "localhost:11434"
```

A project's own `whaleshark.toml` can name which tool its agents use, and never describe one: only
you can wire in a program on your machine.

## Sources

Read on 22 October 2026. Where a page did not say, the table says "none" or the text says "unknown".

- Claude Code: the built-in description, run under WhaleShark since its first phase.
- Codex: https://github.com/openai/codex, https://learn.chatgpt.com/docs/developer-commands?surface=cli,
  https://learn.chatgpt.com/docs/agent-configuration/agents-md, https://learn.chatgpt.com/docs/config-file/config-advanced,
  https://learn.chatgpt.com/docs/hooks.md
- Gemini CLI: https://github.com/google-gemini/gemini-cli, https://www.geminicli.com/docs/cli/cli-reference,
  https://www.geminicli.com/docs/cli/gemini-md, https://www.geminicli.com/docs/cli/session-management,
  https://developers.googleblog.com/an-important-update-transitioning-gemini-cli-to-antigravity-cli
- Qwen Code: https://github.com/QwenLM/qwen-code, https://qwenlm.github.io/qwen-code-docs/ (Configuration:
  Settings, and Authentication)
- OpenCode: https://opencode.ai/docs/cli/, https://opencode.ai/docs/rules/, https://opencode.ai/docs/providers/,
  https://opencode.ai/docs/permissions/
- Cursor CLI: https://cursor.com/docs/cli/overview, https://cursor.com/docs/cli/reference/parameters,
  https://cursor.com/docs/cli/using, https://cursor.com/docs/cli/changelog
- Hermes Agent: https://github.com/NousResearch/hermes-agent, https://hermes-agent.nousresearch.com/docs/reference/cli-commands,
  https://hermes-agent.nousresearch.com/docs/user-guide/features/context-files, https://hermes-agent.nousresearch.com/docs/integrations/providers
- Goose: https://goose-docs.ai/docs/guides/goose-cli-commands, https://goose-docs.ai/docs/guides/context-engineering/using-goosehints,
  https://goose-docs.ai/docs/getting-started/providers, https://goose-docs.ai/docs/guides/environment-variables
- Crush: https://github.com/charmbracelet/crush
- Amp: https://ampcode.com/docs/cli, https://ampcode.com/docs/customize/agents-md, https://ampcode.com/docs/markdown/the-dial,
  https://ampcode.com/docs/markdown/tools, https://ampcode.com/docs/markdown/customize/model-routing
- Aider: https://aider.chat/docs/config/options.html, https://aider.chat/docs/usage/conventions.html,
  https://aider.chat/docs/llms/ollama.html, https://aider.chat/docs/llms/openai-compat.html
- Grok Build: https://docs.x.ai/build/overview, https://github.com/xai-org/grok-build (its user guide: project rules,
  sessions, custom models)
- Ollama: https://docs.ollama.com/api/openai-compatibility, https://docs.ollama.com/faq
- LM Studio: https://lmstudio.ai/docs/developer/openai-compat
- llama.cpp: https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md
- vLLM: https://docs.vllm.ai/en/latest/serving/online_serving/openai_compatible_server/
