# nostr

A comprehensive Go library for the Nostr protocol, providing everything needed to build relays, clients, or hybrid applications.

This is a fork of [nostrlib](https://gitworkshop.dev/fiatjaf.com/nostrlib) with the following custom optimizations:

- **Removed CGO dependencies**: Pure Go implementation for better cross-platform compatibility
- **Additional features**: improved API design

## Installation

```sh
go get -u github.com/ClarkQAQ/nostr
go mod edit -replace fiatjaf.com/nostr=github.com/ClarkQAQ/nostr@latest
go mod tidy
```

## Components

- **eventstore**: Pluggable storage backends (Bluge, BoltDB, LMDB, in-memory, nullstore)
- **khatru**: Relay framework for building Nostr relays
- **sdk**: Client SDK with caching, data loading, and relay management
- **keyer**: Key management utilities
- **NIPs**: Implementations for NIPs 4-94, covering encryption, metadata, relays, and more