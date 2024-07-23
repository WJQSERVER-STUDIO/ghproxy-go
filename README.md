# ghProxy-Go

## 简介
GitHub 代理服务，支持 Github release、archive以及项目文件

## 部署

```
docker run -d -p 80:80 -v ./config:/data/ghproxy/config -v ./log:/data/ghproxy/log --restart always wjqserver/ghproxy:latest
```
