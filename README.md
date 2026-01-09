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

- **eventstore**: Pluggable storage backends (BadgerDB, BoltDB, in-memory)
- **relay**: Flexible framework for building Nostr relays
- **relay/blossom**: Plugin for a relay server that adds flexible Blossom server support
- **relay/grasp**: Plugin for a relay server that adds Grasp server support
- **sdk**: Client SDK with caching, data loading, and outbox relay management
- **keyer**: Key and bunker management utilities
