#!/bin/bash
set -e

SQUAD_DIR="/root/observing-squad-bon"
echo "Setting up local observing squad at $SQUAD_DIR"

mkdir -p $SQUAD_DIR/{node-0,node-1,node-2,node-metachain,proxy}/config
mkdir -p $SQUAD_DIR/{node-0,node-1,node-2,node-metachain}/{db,logs}

# Copy the exact Battle of Nodes configuration to each node
cp -r /root/elrond-nodes/node-0/config/* $SQUAD_DIR/node-0/config/
cp -r /root/elrond-nodes/node-0/config/* $SQUAD_DIR/node-1/config/
cp -r /root/elrond-nodes/node-0/config/* $SQUAD_DIR/node-2/config/
cp -r /root/elrond-nodes/node-0/config/* $SQUAD_DIR/node-metachain/config/

# In prefs.toml, destination shards need to be fixed for each node
sed -i 's/DestinationShardAsObserver = "disabled"/DestinationShardAsObserver = "0"/' $SQUAD_DIR/node-0/config/prefs.toml
sed -i 's/DestinationShardAsObserver = "disabled"/DestinationShardAsObserver = "1"/' $SQUAD_DIR/node-1/config/prefs.toml
sed -i 's/DestinationShardAsObserver = "disabled"/DestinationShardAsObserver = "2"/' $SQUAD_DIR/node-2/config/prefs.toml
sed -i 's/DestinationShardAsObserver = "disabled"/DestinationShardAsObserver = "metachain"/' $SQUAD_DIR/node-metachain/config/prefs.toml

# Set node names
sed -i 's/NodeDisplayName = "SuperRareBears_BoN"/NodeDisplayName = "Observer_S0"/' $SQUAD_DIR/node-0/config/prefs.toml
sed -i 's/NodeDisplayName = "SuperRareBears_BoN"/NodeDisplayName = "Observer_S1"/' $SQUAD_DIR/node-1/config/prefs.toml
sed -i 's/NodeDisplayName = "SuperRareBears_BoN"/NodeDisplayName = "Observer_S2"/' $SQUAD_DIR/node-2/config/prefs.toml
sed -i 's/NodeDisplayName = "SuperRareBears_BoN"/NodeDisplayName = "Observer_Meta"/' $SQUAD_DIR/node-metachain/config/prefs.toml

# Proxy configuration
cat << 'PROXY_CONFIG' > $SQUAD_DIR/proxy/config/config.toml
[ApiLogging]
    LogRequests = false

[GeneralSettings]
    RequestTimeoutSec = 10
    HeartbeatCacheValidityDurationInSec = 60
    ValStatsRouteEnabled = false
    FaucetValue = "0"

[Observers]
    [[Observers.ShardIDs]]
        ShardId = 0
        Address = "http://observer-0:8080"
    [[Observers.ShardIDs]]
        ShardId = 1
        Address = "http://observer-1:8080"
    [[Observers.ShardIDs]]
        ShardId = 2
        Address = "http://observer-2:8080"
    [[Observers.ShardIDs]]
        ShardId = 4294967295
        Address = "http://observer-meta:8080"
PROXY_CONFIG

cat << 'COMPOSE' > $SQUAD_DIR/docker-compose.yml
version: '3.8'

x-node-common: &node-common
  image: multiversx/multiversx-node-mainnet:latest
  command: --destination-shard-as-observer=${SHARD:-0}

services:
 observer-0:
   image: multiversx/chain-observer:latest
   ports: [ "8080:8080", "37373:37373" ]
   volumes:
     - ./node-0/db:/go/mx-chain-go/cmd/node/db
     - ./node-0/logs:/go/mx-chain-go/cmd/node/logs
     - ./node-0/config:/config
   command: --destination-shard-as-observer=0

 observer-1:
   image: multiversx/chain-observer:latest
   ports: [ "8081:8080", "37374:37373" ]
   volumes:
     - ./node-1/db:/go/mx-chain-go/cmd/node/db
     - ./node-1/logs:/go/mx-chain-go/cmd/node/logs
     - ./node-1/config:/config
   command: --destination-shard-as-observer=1

 observer-2:
   image: multiversx/chain-observer:latest
   ports: [ "8082:8080", "37375:37373" ]
   volumes:
     - ./node-2/db:/go/mx-chain-go/cmd/node/db
     - ./node-2/logs:/go/mx-chain-go/cmd/node/logs
     - ./node-2/config:/config
   command: --destination-shard-as-observer=2

 observer-meta:
   image: multiversx/chain-observer:latest
   ports: [ "8083:8080", "37376:37373" ]
   volumes:
     - ./node-metachain/db:/go/mx-chain-go/cmd/node/db
     - ./node-metachain/logs:/go/mx-chain-go/cmd/node/logs
     - ./node-metachain/config:/config
   command: --destination-shard-as-observer=metachain

 proxy:
   image: multiversx/chain-proxy:latest
   ports: [ "7950:7950" ]
   volumes:
     - ./proxy/config:/data/config
   restart: unless-stopped
COMPOSE

cd $SQUAD_DIR
echo "Starting squad..."
docker compose up -d

echo "Done!"
EOF
