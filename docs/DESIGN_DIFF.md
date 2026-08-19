# tool/diff implementation design (SPEC_DIFF)

The spec is authoritative; this records the decisions it leaves open.
PR A is the store arms, shippable alone; PR B is the tool.

## the undecodable-args write path (PR A)

`RecordToolCall` canonicalizes, then writes. An undecodable args string
lands raw and the call returns the decode error even though the row was
written: the recorder's existing loud voice (`tool call <id>: <err>`) is
exactly the line the spec names, and a loud return after a successful
write keeps "not a new channel, not a Fault". The contract becomes: an
error means the row did not land, or it landed raw and is not findable
by a canonical query.

## the engine (PR B)

Linear-space Myers (divide and conquer on the middle snake): O(ND) time,
O(N+M) space. This is the "not an LCS DP table" decision; the tie-break
follows diffutils (prefer down only when V[k+1] is strictly greater than
V[k-1]), verified against git on ambiguous fixtures. A region is a
maximal run of edits; context is 3; regions merge when 2*context or
fewer matched lines separate them (verified against git -U3: a gap of 6
merges, a gap of 7 splits). Hunk headers take the diffutils rule: a
count of 1 on a non-empty range drops the count, an empty range prints
`k,0`. Deletions precede insertions within a region.

## lines

Split on `\n`; a trailing newline is not a line (the house rule). A
diff that differs only in a trailing newline is `identical`.

## verb decoding

Per-field extraction with loud voices (the todo `itemsOf` precedent):
the spec's pinned voices, plus same-family voices for type errors on
`ref`, `paths`, and `tool`. Args owned by the other verb are ignored.

## the last header

Timestamps are formatted RFC3339Nano, which round-trips the stored text
exactly ("as stored"). Whole-second timestamps render without a
fraction, as the spec's example shows.

## the wire order

`diff` appends at the end of the WithTools list and of the allow-list
default (python, web_search, web_fetch all appended at the end). The
golden fixtures regenerate after registration; the diff of the
regeneration is read before it is committed.

## the files shell-out

`exec.CommandContext` plus `WaitDelay(1s)` (the bash precedent). The cwd
is the process cwd (`os.Getwd`), named in the not-git voice. "not a git
repository" is matched case-insensitively: git's two failure voices
differ in case.
