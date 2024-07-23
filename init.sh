#!/bin/bash

if [ ! -f /data/caddy/config/Caddyfile ]; then
    cp /data/caddy/Caddyfile /data/caddy/config/Caddyfile
fi

/data/caddy/caddy run --config /data/caddy/config/Caddyfile > /data/ghproxy/log/caddy.log 2>&1 &

/data/ghproxy/ip > /data/ipinfo/ghproxy/ip.log 2>&1 &

while [[ true ]]; do
    sleep 1
done    

