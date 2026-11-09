# rig: a model's reasoning stays its own

The session store showed it (2026-11-08, session 1a1be29a…): the operator chatted with DeepSeek
(`deepseek-1017-ud-l`, osmos), then switched to Qwen3.8 (`qwen3.8-flash-next-1m`). Every assistant message carried
its reasoning, and rig sent all of it back with the history. Qwen's chat template keeps prior thinking in the
prompt, so Qwen read DeepSeek's terse reasoning ("The user is just chatting. No task. Keep it brief, don't force
work.") as its own past turns. Its next turn copied it ("Still just chatting. Keep it light, brief, honest. Don't
push work.") and ended with no answer, three times (SPEC_EMPTY).

## decision

A request carries a turn's reasoning only when the model row it goes to wrote that turn. Another row's turns go
with their text, their tool calls and their results, never their reasoning.

- **Where.** `policy/lineage`'s `Decorator(inner, own)`, the outermost provider decorator inside `toolset.Carry`.
  It sees every delta the loop sees (the empty guard's discarded attempts included), so the reasoning it records
  for a stream is the reasoning the loop's message for that stream carries.
- **The record.** `lineage.Own`, one per model row per session, on the root (`ownFor(row)`): the reasoning texts
  the row's streams produced. Every pair built for the row shares it, so a role switch (which rebuilds the pair)
  keeps it, and a switch back to a row finds its own reasoning again. A new session starts with none.
- **Resume.** The current row's record is seeded from the store: `state.TrailingReasoning(session, row)`, the
  reasoning of the trailing run of assistant turns of the last one's model, when the session was last on `row`
  (its `sessions.model`), else none. The first request after a resume is then the request the session would have
  sent, so a server's prompt cache (osmos's kept conversations) still hits.
- **Never the session.** The request's copy loses the reasoning; the session's messages and the store keep
  everything. The store is the transcript of record.
- **Signed reasoning.** A turn with `ReasoningDetails` (Anthropic's thinking signatures, OpenRouter's encrypted
  reasoning) is sent unchanged: those must go back as they came, to the provider that made them.

## why not at the switch

The web frontend runs a command as its request arrives, a turn possibly live, so a `/model` there can switch while
the loop reads the session. Editing the session at the switch would race it; the request-side copy does not.

## layout

```
policy/lineage/   Own, Decorator; PACKAGE.md
store/state/      TrailingReasoning
cmd/rig/          ownFor, ownResumed; buildPair wraps the row's provider; a new session resets, a resume seeds
specs/            this file
```

## tests

- `policy/lineage`: another model's reasoning dropped and the session untouched; the row's own sent back (its
  deltas joined), across an empty turn's resample too; seeded reasoning kept; a second decorator over the same
  record; reasoning details left alone.
- `store/state`: the trailing run of the last model's turns; none for a session last on another row; none for a
  session with no assistant turn.
