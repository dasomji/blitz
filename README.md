# blitz

Fast one-off AI from the terminal, powered by OpenAI Codex subscription auth.

`blitz` is a small Go CLI for quick, non-agentic requests. It reads input from stdin or arguments, sends it to the Codex subscription endpoint, and prints the result without starting a bloated agent harness.

## Why Go

The binary starts quickly, has no runtime package manager, and uses only the Go standard library. It is designed for scripts, text filters, launchers, and other places where you want one model call and then exit.

## Auth

Use your OpenAI/Codex subscription login:

```sh
blitz login
```

Open the printed URL, enter the code, and `blitz` saves OAuth tokens to:

```sh
~/.blitz/auth.json
```

`blitz` also falls back to an existing Codex CLI login at:

```sh
~/.codex/auth.json
```

So this also works:

```sh
codex login
cat input.txt | blitz
```

`blitz` is intentionally subscription-only. It does not support API keys or custom endpoints.

## Usage

```sh
blitz "Tell me a joke"
printf '%s' "$LONG_TEXT" | blitz --summarize
cat notes.md | blitz --prompt "Extract action items. Output bullets only."
blitz status
```

## Defaults

Built-in defaults use `gpt-5.5`, reasoning off, and fast mode on. Codex requires streamed Responses requests, so `blitz` always streams internally and prints output as it arrives. See effective defaults with:

```sh
blitz
blitz status
blitz --help
```

Persist defaults in `~/.blitz/config.json` with `blitz config set`:

```sh
blitz config set model gpt-5.5
blitz config set reasoning off
blitz config set fast true
blitz config set timeout 10m
blitz config unset reasoning
```

Supported config keys: `model`, `codex-home`, `skills-dir`, `prompt`, `reasoning`, `max-output-tokens`, `timeout`, and `fast`.

## Skills

Skills are saved system prompts. Put Markdown files in `~/.blitz/skills`; the filename without `.md` becomes a flag:

```sh
mkdir -p ~/.blitz/skills
cat > ~/.blitz/skills/summarize.md <<'EOF'
Summarize the input clearly. Keep only the most important points. Output bullets.
EOF

blitz --summarize "Long text goes here"
```

Use `-skills-dir` or `BLITZ_SKILLS_DIR` to point at a different skill folder.

### Example: transcript cleanup

A transcript cleanup workflow is just a skill:

```sh
cat > ~/.blitz/skills/transcript.md <<'EOF'
Improve this transcript for readability and accuracy. Fix obvious ASR mistakes,
punctuation, capitalization, speaker-flow issues, and paragraphing. Preserve
meaning. Output only the enhanced transcript.
EOF

blitz --transcript "Hello world ähm hmm here i am"
```

## Fast mode implementation

The user-facing abstraction is `fast`.

As of the current Codex implementation, Codex maps Fast mode to the Responses API request field:

```json
{"service_tier":"priority"}
```

`blitz` follows that behavior: when `fast` is true, it sends `service_tier: "priority"`. There is intentionally no user-facing service-tier setting; if Codex changes how Fast mode is represented, update this section and the `codexRequestBody` implementation.

Codex also currently builds Responses requests with `stream: true`, and the Codex backend rejects `stream: false` with `Stream must be set to true`. `blitz` therefore always sends `stream: true` and reads server-sent events (SSE).

Reference checked against `openai/codex`:

- `ServiceTier::Fast.request_value()` returns `"priority"`.
- `ServiceTier::Flex.request_value()` returns `"flex"`.
- `ResponsesApiRequest` is constructed with `stream: true`.
- Fast mode is feature-gated in Codex and defaults enabled there.

## Flags

```text
-model             model name, default gpt-5.5
-prompt            replacement system prompt
-skills-dir        directory of skill markdown prompts
-fast              request Fast mode via service_tier=priority; default true
-reasoning         reasoning effort, default off; use low/medium/high or off/none
-max-output-tokens optional output cap
-timeout           request timeout, default 10m
-codex-home        Codex home containing auth.json
-blitz-home        Blitz home containing auth.json and config.json
```

Environment variables:

```text
BLITZ_MODEL
BLITZ_PROMPT
BLITZ_SKILLS_DIR
BLITZ_REASONING_EFFORT
BLITZ_HOME
CODEX_HOME
```

## Build

This repo was built with Go via `mise`:

```sh
mise x go@1.26.4 -- go test ./...
mise x go@1.26.4 -- go build -o blitz ./cmd/blitz
```

If Go is installed normally:

```sh
go test ./...
go build -o blitz ./cmd/blitz
```

## License

MIT

## Implementation Notes

The Codex subscription path follows the current OpenAI Codex and Pi patterns:

- Codex stores ChatGPT login tokens in `$CODEX_HOME/auth.json`.
- Pi documents ChatGPT Plus/Pro Codex subscription auth through `/login`, with tokens in `~/.pi/agent/auth.json`.
- Both use ChatGPT OAuth tokens and call the Codex Responses endpoint at `https://chatgpt.com/backend-api/codex/responses`.

`blitz` keeps its own login tokens in `~/.blitz/auth.json`, refreshes them when the access token is near expiry, and uses the Codex-compatible headers:

```text
Authorization: Bearer <access_token>
chatgpt-account-id: <account_id>
OpenAI-Beta: responses=experimental
originator: blitz
```
