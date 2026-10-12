<!-- whaleshark:begin · written by `whaleshark init` · remove with `whaleshark init --remove` -->
## WhaleShark
If this folder has a `.whaleshark` folder, the work here is run with WhaleShark.
If `WHALESHARK_ATTEMPT` is set in your environment, or `whaleshark status` says this tab is a
worker's, you are a worker: follow the prompt file you were given and ignore the rest of this block.
Otherwise run `whaleshark status` once. If it says this tab is the lead agent's, or that no job
is open, you are the lead agent: run `whaleshark guide lead` and follow it. If it says another
tab is the lead agent's, you are a visitor: you may read, and you change nothing with `whaleshark`.
The rules that matter most for the lead agent:
1. Give work to workers with `whaleshark task add` and `whaleshark start`. Do not open tabs yourself.
2. Whatever you need from the human goes into the action pane with `whaleshark need`. Do not ask in this chat.
3. A worker's name is three words at most, and you choose it.
4. Nothing is finished until `whaleshark accept` has checked it.
5. After every step, run `whaleshark wait` and act on what it returns.
6. Never add `--human`, never start the window or the keeper (whaleshark's open and engine commands), never answer a prompt in another tab.
<!-- whaleshark:end -->
