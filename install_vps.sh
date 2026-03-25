#!/bin/bash
set -e

echo "Updating packages..."
apt-get update -y
apt-get install -y curl wget git jq build-essential htop ufw

echo "Installing Go..."
if ! command -v go &> /dev/null; then
    wget -q https://go.dev/dl/go1.22.1.linux-amd64.tar.gz
    rm -rf /usr/local/go && tar -C /usr/local -xzf go1.22.1.linux-amd64.tar.gz
    echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.profile
    export PATH=$PATH:/usr/local/go/bin
    rm go1.22.1.linux-amd64.tar.gz
else
    echo "Go already installed."
fi

echo "Installing Docker..."
if ! command -v docker &> /dev/null; then
    curl -fsSL https://get.docker.com -o get-docker.sh
    sh get-docker.sh
    rm get-docker.sh
else
    echo "Docker already installed."
fi

# Check docker compose plugin
if ! docker compose version &> /dev/null; then
    echo "Checking docker-compose plugin..."
    apt-get install -y docker-compose-plugin
fi

echo "Configuring firewall..."
ufw allow 22/tcp
ufw allow 3000/tcp
ufw allow 9090/tcp
ufw --force enable

echo "Deploying Monitoring Stack..."
mkdir -p /opt/monitoring
cd /opt/monitoring

cat << 'COMPOSE' > docker-compose.yml
version: '3.8'
services:
  prometheus:
    image: prom/prometheus:latest
    container_name: prometheus
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml
    network_mode: "host"

  grafana:
    image: grafana/grafana:latest
    container_name: grafana
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=admin
    network_mode: "host"
COMPOSE

cat << 'PROM' > prometheus.yml
global:
  scrape_interval: 2s

scrape_configs:
  - job_name: 'bot_metrics'
    static_configs:
      - targets: ['localhost:2112']
PROM

docker compose up -d

echo "✅ Environment Setup Complete!"
