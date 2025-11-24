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

- **eventstore**: Pluggable storage backends (Bleve, BoltDB, in-memory)
- **khatru**: Flexible framework for building Nostr relays
- **khatru/blossom**: Plugin for a Khatru server that adds flexible Blossom server support
- **khatru/grasp**: Plugin for a Khatru server that adds Grasp server support
- **sdk**: Client SDK with caching, data loading, and outbox relay management
- **keyer**: Key and bunker management utilities
