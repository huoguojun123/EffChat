# Eino Claude adapter patch

This directory contains the production source files from
`github.com/cloudwego/eino-ext/components/model/claude` version `v0.1.20`.
The upstream module is licensed under Apache License 2.0; its original
`LICENSE` is retained in this directory.

EffChat carries one source-level correction in `claude.go`: the stream reader
goroutine uses a local `concatErr` instead of writing the `Stream` method's
named return variable. The upstream implementation otherwise races that write
against the method return whenever an empty Anthropic metadata frame precedes
the first thinking, text, or tool-call frame.

The patch is intentionally isolated through the root backend `go.mod` replace
directive. When an upstream release removes the race, delete this directory
and the replace directive after the Anthropic native integration race test
passes against that release.
