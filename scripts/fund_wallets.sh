set -e
MXPY=/root/.local/bin/mxpy
PROXY=https://gateway.battleofnodes.com

echo 'Sending to Shard 0 Wallet...'
$MXPY tx new --receiver erd12k35xfk0k6en6rfzhfjtvespsm73vhwd3zy4acnwyjlqvrw3c57qtj0rex --value 100000000000000000000 --pem /root/wallets/master.pem --proxy $PROXY --gas-limit 100000 --send
sleep 8

echo 'Sending to Shard 1 Wallet...'
$MXPY tx new --receiver erd17dgrw3udskg4q07wharx938nrssrr00722d8dc8xtml5dmuq9tdstjq33k --value 100000000000000000000 --pem /root/wallets/master.pem --proxy $PROXY --gas-limit 100000 --send
sleep 8

echo 'Sending to Shard 2 Wallet...'
$MXPY tx new --receiver erd1fcdnxc2qklv9c0dh6thr7gzaq90f4r5se6s0alfd0aj6emzn7laqumqzzp --value 100000000000000000000 --pem /root/wallets/master.pem --proxy $PROXY --gas-limit 100000 --send

echo 'Funding complete'
