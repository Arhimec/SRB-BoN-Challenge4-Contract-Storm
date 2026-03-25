<div align="center">

# 🐻 SuperRareBears — Battle of Nodes
### Guild Wars Challenge 4: Contract Storm ⚡

A high-throughput, cross-shard transaction bot and smart contract deployment suite for the **MultiversX Battle of Nodes** network.

[![Built with Go](https://img.shields.io/badge/Built_with-Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev/)
[![MultiversX](https://img.shields.io/badge/Network-MultiversX-1D2124?style=for-the-badge&logo=multiversx&logoColor=white)](https://multiversx.com/)

</div>

---

## 🚀 Overview

This repository contains the `SuperRareBears` guild's implementation for **Challenge 4: Contract Storm**. The goal of this challenge is to maximize smart contract composability transactions across all three shards on the MultiversX network.

### Architecture

1.  **Smart Contracts (`mx-contracts-rs`)**:
    We utilize the `forwarder-blind` reference contract, compiled to WASM and deployed individually on Shards 0, 1, and 2.
2.  **High-Concurrency Bot (`bot/`)**:
    A custom-built, multi-threaded worker bot written in **Go**. It employs local nonce management and ED25519 signing via `mx-sdk-go` to maximize transaction throughput without waiting for block confirmations.

## 🎯 Bot Features

-   **Multi-Shard Workers**: Dedicated parallel workers for Shard 0, Shard 1, and Shard 2.
-   **Local Nonce Management**: Tracks nonces locally to allow hundreds of transactions to be broadcasted simultaneously.
-   **Dynamic Call Types**: Supports all 4 competition interactions:
    -   `blindSync` (Same-shard efficiency)
    -   `blindAsyncV1`
    -   `blindAsyncV2`
    -   `blindTransfExec`
-   **Automated Drain Tracking**: A background goroutine periodically fires `drain` calls on cross-shard contracts to release locked tokens automatically.

## 🛠️ Quick Start

### 1. Requirements

-   [Go 1.22+](https://go.dev/dl/)
-   MultiversX WALLET `.pem` files for all 3 shards.

### 2. Building the Bot

```bash
cd bot
go build -o bon-bot .
```

### 3. Running the Bot

For the competition phase, adjust the interval down for maximum TPS:

```bash
# Run for 2 hours, using auto-strategy, with 10 tx/s per shard
./bon-bot -duration 2h -calltype auto -interval 100 -drain-interval 6000
```

*Set `-interval 0` to uncap the rate limit (warning: requires sufficient WEGLD for gas!).*

## 📁 Repository Structure

```text
.
├── bot/
│   ├── go.mod        # Go dependencies
│   ├── main.go       # Core bot logic & workers
│   └── signer.go     # ED25519 tx signer & broadcaster
├── install_vps.sh    # VPS bootstrapping script (Go, Docker, Monitoring)
└── scripts/          # EGLD/WEGLD funding & deploy helpers
```

---
<div align="center">
  <i>Developed for the MultiversX Guild Wars by <b>SuperRareBears</b></i>
</div>
