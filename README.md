# blitz

Fast transcript enhancement from the terminal.

`blitz` is a small Go CLI that reads transcript text from stdin or arguments, sends it to either Codex subscription auth or an OpenAI-compatible endpoint, and prints the enhanced transcript.

## Why Go

The binary starts quickly, has no runtime package manager, and the implementation uses only the Go standard library. Network latency dominates the total runtime, so `blitz` streams output by default for lower perceived latency.

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
cat transcript.txt | blitz
```

### OpenAI-compatible API key

Use `responses` or `chat` provider mode for ordinary OpenAI-compatible endpoints:

```sh
export OPENAI_API_KEY=sk-...
cat transcript.txt | blitz -provider responses -model gpt-5.4-mini
cat transcript.txt | blitz -provider chat -base-url http://127.0.0.1:11434/v1 -model local-model
```

## Usage

```sh
cat raw.txt | blitz > enhanced.txt
blitz "um this is a raw transcript and it needs punctuation"
blitz -model gpt-5.5 -reasoning low < raw.txt
blitz -fast=false -service-tier "" < raw.txt
```

## Skills

Skills are saved system prompts. Put Markdown files in `~/.blitz/skills`; the filename without `.md` becomes a flag:

```sh
mkdir -p ~/.blitz/skills
cat > ~/.blitz/skills/transcript.md <<'EOF'
Improve this transcript for readability and accuracy. Fix obvious ASR mistakes,
punctuation, capitalization, speaker-flow issues, and paragraphing. Preserve
meaning. Output only the enhanced transcript.
EOF

blitz --transcript "Hello world ähm hmm here i am"
```

Use `-skills-dir` or `BLITZ_SKILLS_DIR` to point at a different skill folder.

Useful flags:

```text
-provider          codex, responses, or chat
-model             model name, default gpt-5.4-mini
-base-url          OpenAI-compatible base URL
-prompt            replacement transcript enhancement prompt
-skills-dir        directory of skill markdown prompts
-stream            stream output as it arrives, default true
-fast              for codex, request priority service tier when unset
-service-tier      explicit Responses service_tier
-reasoning         reasoning effort, default low
-max-output-tokens optional output cap
```

Environment variables:

```text
BLITZ_PROVIDER
BLITZ_MODEL
BLITZ_BASE_URL
BLITZ_PROMPT
BLITZ_SKILLS_DIR
BLITZ_SERVICE_TIER
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
