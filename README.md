# Surfbot Agent

Local security scanner with pluggable detection and remediation. Open source under MIT license.

> **Early development.** The agent is being built. See [ADR-001](../surfbot-strategy/ADR-001-surfbot-agent-architecture.md) for the architecture vision.

## Quick Start

(coming soon)

## Build from Source

```bash
git clone https://github.com/surfbot-io/surfbot-agent.git
cd surfbot-agent
make build
./bin/surfbot version
```

## Commands

```
surfbot scan [target]        Run detection pipeline
surfbot target add/list/rm   Manage monitored targets
surfbot findings             List discovered vulnerabilities
surfbot assets               List discovered assets
surfbot fix <id>             Apply remediation
surfbot score                Security score
surfbot status               Agent status
surfbot tools                Manage detection tools
surfbot version              Version info
```

## Architecture

See [ADR-001](../surfbot-strategy/ADR-001-surfbot-agent-architecture.md).

## License

MIT - see [LICENSE](LICENSE).
