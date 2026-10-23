<!-- whaleshark:begin · written by `whaleshark init` · remove with `whaleshark init --remove` -->
## WhaleShark
Only for a folder that has a `.whaleshark` folder; anywhere else this block says nothing.
- `WHALESHARK_ATTEMPT` is set, or `whaleshark status` says this tab is a worker's: you are a
  worker (or a fleet leader). Follow the prompt file you were given, not this block.
- Otherwise run `whaleshark status` once. This tab is the lead agent's, or no job is open: you are
  the lead agent; run `whaleshark guide lead` and follow it. Another tab is the lead agent's: you
  are a visitor, and change nothing with `whaleshark`.
- The lead agent gives work out with `whaleshark task add` and `whaleshark start`, puts what it
  needs from the person in the action pane with `whaleshark need`, runs `whaleshark wait` after
  every step, and calls nothing finished before `whaleshark accept` has checked it.
- Never add `--human`, never start the window or the keeper (whaleshark's open and engine
  commands), never answer a prompt in another tab.
<!-- whaleshark:end -->
