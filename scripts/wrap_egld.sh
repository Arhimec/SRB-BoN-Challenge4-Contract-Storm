set -e
MXPY=/root/.local/bin/mxpy
PROXY=https://gateway.battleofnodes.com
WEGLD_SC="erd1qqqqqqqqqqqqqpgqhe8t5jewej70zupmh44jurgn29ps8605vnqqx5smv8"

echo "Wrapping 90 EGLD into WEGLD for Shard 0 Wallet..."
$MXPY tx new --receiver $WEGLD_SC --value 90000000000000000000 --pem /root/wallets/shard0.pem --proxy $PROXY --gas-limit 5000000 --data "wrapEgld" --send
sleep 8

echo "Wrapping 90 EGLD into WEGLD for Shard 1 Wallet..."
$MXPY tx new --receiver $WEGLD_SC --value 90000000000000000000 --pem /root/wallets/shard1.pem --proxy $PROXY --gas-limit 5000000 --data "wrapEgld" --send
sleep 8

echo "Wrapping 90 EGLD into WEGLD for Shard 2 Wallet..."
$MXPY tx new --receiver $WEGLD_SC --value 90000000000000000000 --pem /root/wallets/shard2.pem --proxy $PROXY --gas-limit 5000000 --data "wrapEgld" --send

echo "Wrapping complete!"
