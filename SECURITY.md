# Security

rig is a harness that runs untrusted model output with filesystem and shell
access. The design assumes the model is adversarial; every surface below is
guarded and fails closed. This file is the trust model and the reporting
path.

## reporting

Report a vulnerability privately through GitHub's private security advisory
flow (the repo's Security tab → "Report a vulnerability"). Include the rig
version (`rig -version`), how to reproduce, and what the impact is. Do not
open a public issue first.

The fix cycle is: confirm, fix with a named-case test, release, disclose.
There is no bug bounty and no guaranteed response window; this is one
person's daily driver.

## the trust model

- **the model is untrusted.** Everything the model says, and every tool call
  it makes, is input to a boundary: the allow-list, the approval gate, the
  retry guard, the result cap, the path boundary, and the plugin landing
  zone.
- **the operator is trusted.** The operator's settings, themes, plugins, and
  approval mode are the operator's own decisions. A model cannot reach them:
  plugins install only into `plugins/pending/` and go live only on the
  operator's `/plugins approve`.
- **the endpoint is trusted.** The model endpoint receives the transcript.
  Use a loopback endpoint (the default) or your own gateway; anything that
  sees the traffic sees the session.

## the boundaries

- **web_fetch** resolves and pins the dial: private, loopback, link-local,
  multicast, and reserved ranges are refused before the request, and every
  redirect hop is re-validated.
- **bash** runs under a context bound; the process group is killed on
  cancel and the output is capped.
- **the worker jail** is bubblewrap: unshare-all, clearenv, a named setenv
  list, tmpfs `/tmp`, read-only system bindings, and only the workspace,
  the state dir, and the kernel socket writable. A run without bwrap fails
  closed.
- **the plugin provenance rule**: `write` and `edit` into `plugins/` outside
  `plugins/pending/` is refused, symlink-resolved.
- **the dashboard** binds loopback only (anything else refuses at startup)
  and requires a 32-byte bearer token, stored `0600`, compared in constant
  time.
- **updates** are verified against a pinned minisign (ed25519) key; a
  missing key, an unsigned release, or a bad signature refuses before the
  old binary is touched.
- **config** refuses unknown keys and out-of-range bounds by name at
  startup.

## out of scope

- The model being malicious inside the jail or inside the session: that is
  the design.
- The operator's own choices: allowing `bash`, approving a plugin, running
  unsandboxed (`sandbox: off`), or exposing the dashboard beyond loopback.
- Denial of service on the operator's own endpoint or machine by the
  operator.

## the release channel

Assets are built with provenance attestations and shipped with
`checksums.txt` and minisign signatures. Verify an asset against
`checksums.txt`, and check the signature against the pinned key in
`config/settings.json` (`updateKey`). `rig -update` does both; a build
without the pinned key refuses to update.
