# Eino Claude adapter patch

This directory contains the production source files from
`github.com/cloudwego/eino-ext/components/model/claude` version `v0.1.20`.
The upstream module is licensed under Apache License 2.0; its original
`LICENSE` is retained in this directory.

EffChat carries isolated source-level corrections in `claude.go`: the stream reader
goroutine uses a local `concatErr` instead of writing the `Stream` method's
named return variable. The upstream implementation otherwise races that write
against the method return whenever an empty Anthropic metadata frame precedes
the first thinking, text, or tool-call frame.

Cache creation tokens are preserved in message and callback usage. Stream usage
is normalized into cumulative snapshots because Anthropic separates input/cache
counts from output counts, while Eino concatenation takes maxima and the product
recorder consumes the last usage frame. Snapshots retain prompt/cache counts and
recompute totals without adding cumulative output counts twice.

Checked against upstream `v0.1.26` on 2026-10-24: that release includes cache
write mappings but still writes the named stream return error from its reader
goroutine. Upgrading solely to remove this fork is therefore insufficient.

The verified MiniMax M3 and M3.1 Flash compatible endpoints also require replay
of unsigned thinking blocks in tool continuations. That serialization exception
is restricted to those exact model IDs; Claude still requires its original
signature and no signature is fabricated. Both protocols have HTTP fixtures in
the root backend; this does not establish durable database-history replay.

The patch is intentionally isolated through the root backend `go.mod` replace
directive. When an upstream release removes the race, delete this directory
and the replace directive only after the Anthropic native integration race test
and cache usage HTTP fixtures pass against that release.
