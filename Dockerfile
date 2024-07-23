FROM wjqserver/caddy:latest

RUN mkdir -p /data/www
RUN mkdir -p /data/ghproxy/config
RUN mkdir -p /data/ghproxy/log
RUN wget -O /data/ghproxy/config/config.yaml https://raw.githubusercontent.com/WJQSERVER-STUDIO/ghproxy-go/main/config/config.yaml
RUN wget -O /data/www/index.html https://raw.githubusercontent.com/WJQSERVER-STUDIO/ghproxy-go/main/pages/index.html
RUN wget -O /data/caddy/Caddyfile https://raw.githubusercontent.com/WJQSERVER-STUDIO/ghproxy-go/main/Caddyfile
RUN VERSION=$(curl -s https://raw.githubusercontent.com/WJQSERVER-STUDIO/ghproxy-go/main/VERSION) && \
    wget -O /data/ghproxy/ghproxy https://github.com/WJQSERVER-STUDIO/ghproxy-go/releases/download/$VERSION/ip
RUN wget -O /usr/local/bin/init.sh https://raw.githubusercontent.com/WJQSERVER-STUDIO/ghproxy-go/main/init.sh
RUN chmod +x /data/ghproxy/ghproxy
RUN chmod +x /usr/local/bin/init.sh

CMD ["/usr/local/bin/init.sh"]

