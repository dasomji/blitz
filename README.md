# blitz

Fast one-off AI from the terminal, powered by Codex subscription auth.

`blitz` is a small Go CLI for quick, non-agentic requests. It reads input from stdin or arguments, sends it to Codex subscription auth or an OpenAI-compatible endpoint, and prints the result without starting a bloated agent harness.

## Why Go

The binary starts quickly, has no runtime package manager, and uses only the Go standard library. It is designed for scripts, text filters, launchers, and other places where you want one model call and then exit.

## Auth Modes

### Codex subscription login

Use this when you want ChatGPT/Codex subscription auth instead of an API key:

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

### OpenAI-compatible API key

Use `responses` or `chat` provider mode for ordinary OpenAI-compatible endpoints:

```sh
export OPENAI_API_KEY=sk-...
cat input.txt | blitz -provider responses -model gpt-5.5
cat input.txt | blitz -provider chat -base-url http://127.0.0.1:11434/v1 -model local-model
```

## Usage

```sh
blitz "Tell me a joke"
printf '%s' "$LONG_TEXT" | blitz --summarize
cat notes.md | blitz --prompt "Extract action items. Output bullets only."
blitz status
```

## Defaults

Built-in defaults use `gpt-5.5`, reasoning off, fast mode on, and streaming off. See effective defaults with:

```sh
blitz
blitz status
blitz --help
```

Persist defaults in `~/.blitz/config.json` with `blitz config set`:

```sh
blitz config set model gpt-5.5
blitz config set reasoning off
blitz config set stream false
blitz config set fast true
blitz config set timeout 10m
blitz config unset reasoning
```

Supported config keys: `provider`, `model`, `base-url`, `codex-home`, `skills-dir`, `prompt`, `reasoning`, `max-output-tokens`, `timeout`, `stream`, and `fast`.

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

`blitz` follows that behavior: when `fast` is true and the Codex provider is used, it sends `service_tier: "priority"`. There is intentionally no user-facing service-tier setting; if Codex changes how Fast mode is represented, update this section and the `codexRequestBody` implementation.

Reference checked against `openai/codex`:

- `ServiceTier::Fast.request_value()` returns `"priority"`.
- `ServiceTier::Flex.request_value()` returns `"flex"`.
- Fast mode is feature-gated in Codex and defaults enabled there.

## Flags

```text
-provider          codex, responses, or chat
-model             model name, default gpt-5.5
-base-url          OpenAI-compatible base URL
-prompt            replacement system prompt
-skills-dir        directory of skill markdown prompts
-stream            stream output as it arrives, default false
-fast              for codex, request Fast mode via service_tier=priority; default true
-reasoning         reasoning effort, default off; use low/medium/high or off/none
-max-output-tokens optional output cap
```

Environment variables:

```text
BLITZ_PROVIDER
BLITZ_MODEL
BLITZ_BASE_URL
BLITZ_PROMPT
BLITZ_SKILLS_DIR
BLITZ_REASONING_EFFORT
BLITZ_HOME
CODEX_HOME
OPENAI_API_KEY
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
