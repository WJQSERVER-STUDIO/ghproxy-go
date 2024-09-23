# ghProxy-Go

## 简介
GitHub 代理服务，支持 Github release、archive以及项目文件

## 用户鉴权

在config.yaml中使auth为true时,将会开启用户鉴权,开启后须在路径后加上参数?auth_token=yourAuthToken

## 部署

```
docker run -d -p 80:80 -v ./config:/data/ghproxy/config -v ./log:/data/ghproxy/log --restart always wjqserver/ghproxy:latest
```

## TODO

- [x] Git Clone支持
- [x] 用戶鑒權
- [x] 使用API獲取配置信息
