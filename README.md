<div align="center">

# 🐻 SuperRareBears — Battle of Nodes
### Guild Wars Challenge 4: Contract Storm ⚡

A high-throughput, distributed bot fleet for the **MultiversX Battle of Nodes** network.

[![Built with Go](https://img.shields.io/badge/Built_with-Go-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev/)
[![MultiversX](https://img.shields.io/badge/Network-MultiversX-1D2124?style=for-the-badge&logo=multiversx&logoColor=white)](https://multiversx.com/)

</div>

---

## 🚀 Overview

The `SuperRareBears` fleet is a distributed system designed to maximize smart contract composability transactions for **Challenge 4: Contract Storm**. It scales across multiple VPS nodes to hit 500+ TPS across all three shards.

### 🌐 Distributed Architecture

The fleet is distributed across 3 high-performance VPS nodes, each handling a specific shard to maximize parallel processing and avoid gateway rate limits:

| Role | Node IP | Responsibility | Wallets |
| :--- | :--- | :--- | :--- |
| **Master** | `173.249.39.152` | Shard 1 + TUI + Prometheus | 49 |
| **Worker 1** | `84.247.173.193` | Shard 0 | 25 |
| **Worker 2** | `84.247.131.137` | Shard 2 | 25 |

---

## 🎯 System Components

### 1. High-Concurrency Bot (`bot/`)
The core engine is a multi-threaded Go bot that manages 99 wallets.
- **Shard-Specific Loading**: Optimized to load only local shard PEMs on each VPS.
- **Bulk Broadcast**: Uses optimized transaction batching for maximum delivery.
- **Dynamic Call Types**: Cycles through `blindSync`, `blindAsyncV1`, `blindAsyncV2`, and `blindTransfExec`.

### 2. Unified Monitoring (`tui_unified.go`)
A branded terminal dashboard that aggregates metrics from all 3 nodes via Prometheus.
- **Real-time TPS**: Live tracking of fleet-wide throughput.
- **Qualification Progress**: tracks the 300-transaction minimum requirement per interaction type.

### 3. Operational Suite
- `drain_all.go`: Sweeps EGLD, WEGLD, and USDC from all 99 wallets to a master address.
- `disperse_final.go`: Automates funding across the entire fleet.
- `prometheus.yml`: Configuration for the cross-node monitoring stack.

---

## 🛠️ Operational Guide

### 1. Draining Wallets
Before starting a fresh run, ensure all wallets are clean:
```bash
./drain_all /root/wallets/shardX <MASTER_ADDR>
```

### 2. Launching the Attack
On each VPS, launch the bot using the optimized BoN settings:
```bash
nohup ./bot_bin -duration=60m -proxy=https://gateway.battleofnodes.com -interval=500 -batch-size=3 -boost-limit=3000 -boost-gas-price=2000000000 > bot.log 2>&1 &
```

### 3. Monitoring Progress
SSH into the **Master Node** (VPS 1) and run the unified TUI:
```bash
./tui_bin
```

---

## 🤖 AI Limitations & The "Bit Failure" Log

While the bot architecture is robust, it's worth noting the AI assistant's *incapabilities* and "bit failures" that added to the project's character along the way:
- **Chain ID Amnesia:** The AI initially assumed the Battle of Nodes chain ID was `T` (Testnet) or `D` (Devnet) instead of the correct `B`, causing failed transaction broadcasts.
- **`mxpy` Syntax Struggles:** Repeatedly tried to use deprecated or ambiguous `mxpy contract upgrade` flags (like `--recall-nonce`), leading to rejected deploy transactions.
- **Lost in the File System:** Wasted time searching for contract WASM files and Owner PEMs entirely on the local machine instead of checking the distributed VPS nodes where they actually lived.

*Proof that even with an advanced AI copilot, human supervision (and a lot of patience) remains the real MVP.*

---
## 📁 Repository Structure

```text
.
├── bot/                # Core bot engine
├── genwallets/         # Wallet generation utility
├── scripts/            # Deployment & funding helpers
├── drain_all.go        # Wallet cleanup utility
├── disperse_final.go   # Mass funding utility
├── tui_unified.go      # Unified dashboard source
├── install_vps.sh      # Environment bootstrap
└── prometheus.yml      # Monitoring config
```

---
<div align="center">
  <i>Developed for the MultiversX Guild Wars by <b>SuperRareBears</b></i>
</div>
