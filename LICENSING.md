# Licensing

Nudgebee is **source-available** software licensed under the
[Business Source License 1.1](./LICENSE) (BSL 1.1). It is **not** OSI "open
source," but each version automatically converts to Apache 2.0 four years after
its release (see [Change Date / Change License](#change-license)).

> **This document is a plain-language summary for convenience only. The
> [`LICENSE`](./LICENSE) file is the binding legal text. If the two ever
> disagree, `LICENSE` controls.**

## What you CAN do for free

- Read, study, and audit the source.
- Modify it and create derivative works.
- Redistribute it (with this License attached).
- **Run it in production for your own internal purposes** — operating,
  observing, and managing your own systems and infrastructure.
- Evaluate it, build on it, and contribute back.

## What you CANNOT do without a commercial license

- Offer Nudgebee (or a substantial part of its functionality) to third parties
  as a **hosted or managed service**.
- Use Nudgebee to **deliver a commercial product or service to your customers or
  clients** — including managed, consulting, outsourced, or systems-integration
  engagements run on their behalf.

In short: **run it for yourself, freely. Run it *for other people* as a
business, talk to us first.** If you're a services or SI firm wanting to deploy
Nudgebee for your clients, that's a partnership — reach out at
**licensing@nudgebee.com**.

## Change License

The BSL applies **separately to each released version**, and each version
converts to the **Apache License, Version 2.0** (kept in this repo at
[`licenses/Apache-2.0.txt`](./licenses/Apache-2.0.txt)) **four years after that
version is published** — on its own clock.

So the source you ship today frees up ~4 years from today's release; a version
you ship next year frees up ~4 years from *its* release. Your latest code always
stays ~4 years ahead of the conversion line; only older versions become Apache.

> **Maintainer note:** the `Change Date` in `LICENSE` is the rolling phrase
> "Four years from the date the Licensed Work is published," so it needs **no
> per-release editing** — the four-year clock runs automatically from each
> version's publication date.

## History

Versions of Nudgebee published before this License took effect were released
under Apache 2.0 and remain available under those terms. BSL 1.1 applies to
releases from this change forward.

## Contributing

Contributions are accepted under the project's [Contributor License Agreement
(`CLA.md`)](./CLA.md). The CLA grants Nudgebee a sublicensable license "under any
license," which is what lets the Licensor offer Nudgebee under both the BSL and
separate commercial licenses. It covers each contributor's past, present, and
future contributions.

### CLA enforcement (single source of truth)

Signatures are checked by the [`contributor-assistant` GitHub Action](./.github/workflows/cla.yaml),
which uses **`CLA.md` in this repo** as the document contributors agree to — so
the signed text and the repo file can never drift apart. Signatures are recorded
in the `cla-signatures` branch.

**One-time setup:** create a Personal Access Token with rights to commit to this
repo's signatures branch (classic token with `repo` scope, or a fine-grained
token with Contents + Pull requests read/write) and store it as the
`CLA_SIGNATURES_TOKEN` repository secret. The default `GITHUB_TOKEN` cannot push
to the signatures branch.

**To require everyone to re-sign** (e.g. after a material CLA change, or to retire
signatures gathered against a prior/incorrect CLA): bump the version segment in
`path-to-signatures` in `cla.yaml` (`version1` → `version2`). The bot will then
treat all prior signatures as invalid and re-prompt every contributor on their
next PR.

## Per-file header

New source files should carry a short SPDX-style header (adapt the comment
syntax per language):

```
// Copyright (c) 2026 Nudgebee CloudXP Pvt Ltd
// Use of this source code is governed by the Business Source License 1.1
// that can be found in the LICENSE file, or at https://mariadb.com/bsl11.
```
